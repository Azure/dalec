package main

import (
	"bufio"
	"bytes"
	"encoding/json"
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
)

func main() {
	tmp, err := os.MkdirTemp("", "test2json2gha-")
	if err != nil {
		panic(err)
	}

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

	anyFail, err := do(os.Stdin, os.Stdout, mod)
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

// TestEvent is the go test2json event data structure we receive from `go test`
// This is defined in https://pkg.go.dev/cmd/test2json#hdr-Output_Format
type TestEvent struct {
	Time    time.Time
	Action  string
	Package string
	Test    string
	Elapsed float64 // seconds
	Output  string
}

// TestResult is where we collect all the data about a test
type TestResult struct {
	output  *os.File
	failed  bool
	pkg     string
	name    string
	elapsed float64
}

func (r *TestResult) Close() {
	r.output.Close()
}

func do(in io.Reader, out io.Writer, modName string) (bool, error) {
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

		if te.Test == "" {
			// Don't bother processing events that aren't specifically for a test
			// Go adds extra events in for package level info that we don't need.
			continue
		}

		tr, err := getOutputStream()
		if err != nil {
			return false, err
		}
		if err := handlEvent(te, tr); err != nil {
			slog.Error("Error handing event test event", "error", err)
		}
	}

	buf := bufio.NewWriter(out)
	var anyFail bool

	for _, tr := range outs {
		if tr.failed {
			anyFail = true
		}

		if err := writeResult(tr, buf, modName); err != nil {
			slog.Error("Error writing result", "error", err)
			continue
		}

		if err := buf.Flush(); err != nil {
			slog.Error(err.Error())
		}
	}

	return anyFail, nil
}

func handlEvent(te *TestEvent, tr *TestResult) error {
	if te.Output != "" {
		_, err := tr.output.Write([]byte(te.Output))
		if err != nil {
			return errors.Wrap(err, "error collecting test event output")
		}
	}

	tr.pkg = te.Package
	tr.name = te.Test
	tr.elapsed = te.Elapsed

	if te.Action == "fail" {
		tr.failed = true
	}
	return nil
}

func writeResult(tr *TestResult, out io.Writer, modName string) error {
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

	dur := time.Duration(tr.elapsed * float64(time.Second))
	fmt.Fprintln(out, "::group::"+prefix+group, dur)
	defer func() {
		fmt.Fprintln(out, "::endgroup::")
	}()

	dt, err := io.ReadAll(tr.output)
	if err != nil {
		return fmt.Errorf("error reading test output: %w", err)
	}

	if !tr.failed {
		if _, err := out.Write(dt); err != nil {
			return fmt.Errorf("error writing test output to output stream: %w", err)
		}
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(dt))

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
