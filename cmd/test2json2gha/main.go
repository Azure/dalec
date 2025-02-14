package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/vearutop/dynhist-go"
)

func main() {
	tmp, err := os.MkdirTemp("", "test2json2gha-")
	if err != nil {
		panic(err)
	}

	var slowThreshold time.Duration
	flag.DurationVar(&slowThreshold, "slow", 500*time.Millisecond, "Threshold to mark test as slow")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	info, _ := debug.ReadBuildInfo()
	var mod string
	if info != nil {
		mod = info.Main.Path
	}

	// Set TMPDIR so that [os.CreateTemp] can use an empty string as the dir
	// and wind up in our dir.
	os.Setenv("TMPDIR", tmp)

	cleanup := func() { os.RemoveAll(tmp) }

	anyFail, err := do(os.Stdin, os.Stdout, mod, slowThreshold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%+v", err)
		cleanup()
		os.Exit(1)
	}

	cleanup()

	if anyFail {
		// In case pipefail is not enabled, make sure we exit non-zero
		os.Exit(2)
	}
}

func do(in io.Reader, out io.Writer, modName string, slowThreshold time.Duration) (bool, error) {
	dec := json.NewDecoder(in)

	te := &TestEvent{}

	outs := make(map[string]*TestResult)
	defer func() {
		for _, tr := range outs {
			tr.Close()
		}
	}()

	getOutputStream := func() (*TestResult, error) {
		key := path.Join(te.Package, te.Test)
		tr := outs[key]
		if tr == nil {
			f, err := os.CreateTemp("", strings.Replace(key, "/", "-", -1))
			if err != nil {
				return nil, errors.WithStack(err)
			}
			tr = &TestResult{output: f}
			outs[key] = tr
		}
		return tr, nil
	}

	for {
		*te = TestEvent{}
		if err := dec.Decode(te); err != nil {
			if err == io.EOF {
				break
			}
			return false, errors.WithStack(err)
		}

		tr, err := getOutputStream()
		if err != nil {
			return false, err
		}
		if err := collectTestOutput(te, tr); err != nil {
			slog.Error("Error handing event test event", "error", err)
		}
	}

	buf := bufio.NewWriter(out)

	summaryF := getSummaryFile()
	defer summaryF.Close()

	var failCount, skipCount int
	var elapsed float64

	failBuf := bytes.NewBuffer(nil)
	hist := &dynhist.Collector{
		PrintSum:     true,
		WeightFunc:   dynhist.ExpWidth(1.2, 0.9),
		BucketsLimit: 10,
	}

	slowBuf := bytes.NewBuffer(nil)
	slow := slowThreshold.Seconds()

	for _, tr := range outs {
		if tr.skipped {
			skipCount++
		}

		if tr.failed {
			failCount++
		}

		hist.Add(tr.elapsed)
		elapsed += tr.elapsed

		if tr.name == "" {
			// Don't write generic package-level details unless its a failure
			// since there is nothing interesting here.
			if !tr.failed {
				continue
			}
		}

		if err := writeResult(tr, buf, failBuf, modName); err != nil {
			slog.Error("Error writing result", "error", err)
			continue
		}

		if tr.elapsed > slow {
			fmt.Fprintf(slowBuf, "%s %.3fs\n", tr.name, tr.elapsed)
		}

		if err := buf.Flush(); err != nil {
			slog.Error(err.Error())
		}
	}

	fmt.Fprintln(summaryF, "## Test metrics")
	separator := strings.Repeat("&nbsp;", 4)
	fmt.Fprintln(summaryF, mdBold("Skipped:"), skipCount, separator, mdBold("Failed:"), failCount, separator, mdBold("Total:"), len(outs), separator, mdBold("Elapsed:"), fmt.Sprintf("%.3fs", elapsed))

	fmt.Fprintln(summaryF, mdPreformat(hist.String()))

	if failBuf.Len() > 0 {
		fmt.Fprintln(summaryF, "## Failures")
		fmt.Fprintln(summaryF, failBuf.String())
	}

	if slowBuf.Len() > 0 {
		fmt.Fprintln(summaryF, "## Slow Tests")
		fmt.Fprintln(summaryF, slowBuf.String())
	}

	return failCount > 0, nil
}

func (c *nopWriteCloser) Close() error { return nil }

func getSummaryFile() io.WriteCloser {
	// https://docs.github.com/en/actions/writing-workflows/choosing-what-your-workflow-does/workflow-commands-for-github-actions#adding-a-job-summary
	v := os.Getenv("GITHUB_STEP_SUMMARY")
	if v == "" {
		return &nopWriteCloser{io.Discard}
	}

	f, err := os.OpenFile(v, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		slog.Error("Error opening step summary file", "error", err)
		return &nopWriteCloser{io.Discard}
	}
	return f
}

func writeResult(tr *TestResult, out, failBuf io.Writer, modName string) error {
	if tr.name == "" {
		return nil
	}

	if _, err := tr.output.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("error seeking to beginning of test output: %w", err)
	}

	pkg := strings.TrimPrefix(tr.pkg, modName)
	pkg = strings.TrimPrefix(pkg, "/")

	group := pkg
	if group != "" && tr.name != "" {
		group += "."
	}
	group += tr.name
	var prefix string
	if tr.failed {
		// Adds a red X emoji to the front of the group name to more easily spot
		// failures
		prefix = "\u274c "
	}

	fmt.Fprintf(out, "::group::%s %.3fs\n", prefix+group, tr.elapsed)
	defer func() {
		fmt.Fprintln(out, "::endgroup::")
	}()

	var rdr io.Reader = tr.output

	if !tr.failed {
		if _, err := io.Copy(out, rdr); err != nil {
			return fmt.Errorf("error writing test output to output stream: %w", err)
		}
		return nil
	}

	failLog := bytes.NewBuffer(nil)
	rdr = io.TeeReader(rdr, failLog)
	defer func() {
		fmt.Fprintln(failBuf, mdLog(tr.name+fmt.Sprintf(" %3.fs", tr.elapsed), failLog))
	}()

	scanner := bufio.NewScanner(rdr)

	var (
		file, line string
	)

	buf := bytes.NewBuffer(nil)

	for scanner.Scan() {
		txt := scanner.Text()
		f, l, ok := getTestOutputLoc(txt)
		if ok {
			file = f
			line = l
		}

		// %0A is the url encoded form of \n.
		// This allows a multi-line message as an annotation
		// See https://github.com/actions/toolkit/issues/193#issuecomment-605394935
		buf.WriteString(txt + "%0A")
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading test output: %w", err)
	}

	file = filepath.Join(pkg, file)
	fmt.Fprintf(out, "::error file=%s,line=%s::%s\n", file, line, buf)

	return nil
}

func getTestOutputLoc(s string) (string, string, bool) {
	file, other, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", false
	}
	line, _, ok := strings.Cut(other, ":")
	if !ok {
		return "", "", false
	}

	return strings.TrimSpace(file), line, true
}
