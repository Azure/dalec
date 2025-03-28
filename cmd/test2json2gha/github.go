package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"os"
	"strconv"
	"strings"
)

const (
	groupHeader = "::group::"
	groupFooter = "::endgroup::\n"
)

// consoleFormatter writes annotations using the github actions console format
// It creates a gruop for each test.
// Note, the console format does not support nested groupings, so subtests are in their own group.
//
// See https://docs.github.com/en/actions/writing-workflows/choosing-what-your-workflow-does/workflow-commands-for-github-actions
type consoleFormatter struct {
	modName string
	verbose bool
}

func (c *consoleFormatter) FormatResults(results iter.Seq[*TestResult], out io.Writer) error {
	var rdrs []io.Reader

	for tr := range results {
		if tr.name == "" {
			continue
		}

		if !c.verbose && !tr.failed {
			// Skip non-failed tests if not verbose
			continue
		}

		pkg := strings.TrimPrefix(tr.pkg, c.modName)
		pkg = strings.TrimPrefix(pkg, "/")

		group := pkg
		if group != "" && tr.name != "" {
			group += "."
		}
		group += tr.name

		hdr := strings.NewReader(groupHeader + group + "\n")
		output := tr.Reader()
		footer := strings.NewReader(groupFooter)

		rdrs = append(rdrs, io.MultiReader(hdr, output, footer))
	}

	_, err := io.Copy(out, io.MultiReader(rdrs...))
	if err != nil {
		return fmt.Errorf("failed to write console results: %w", err)
	}
	return nil
}

type errorAnnotationFormatter struct{}

func (c *errorAnnotationFormatter) FormatResults(results iter.Seq[*TestResult], out io.Writer) error {
	var rdrs []io.Reader
	for tr := range results {
		if !tr.failed || tr.name == "" {
			continue
		}

		rdrs = append(rdrs, asErrorAnnotations(tr))
	}

	_, err := io.Copy(out, io.MultiReader(rdrs...))
	if err != nil {
		return fmt.Errorf("failed to write error annotations: %w", err)
	}
	return nil
}

func asErrorAnnotations(tr *TestResult) io.Reader {
	return &errorAnnotationReader{
		tr: tr,
	}
}

type errorAnnotationReader struct {
	tr  *TestResult
	rdr io.Reader
}

func (a *errorAnnotationReader) Read(p []byte) (n int, err error) {
	if a.rdr == nil {
		// First get the last file and line number from the output
		file, line, err := getLastFileLine(a.tr.Reader())
		if err != nil {
			return -1, err
		}

		// Create the header
		hdr := strings.NewReader(fmt.Sprintf("::error file=%s,line=%d::", file, line))
		footer := strings.NewReader("\n")

		// Setup the underlying reader
		rdr := bufio.NewReader(a.tr.Reader())
		a.rdr = io.MultiReader(hdr, &urlEncodeNewlineReader{rdr}, footer)
	}

	return a.rdr.Read(p)
}

// urlEncodeNewlineReader is a reader that replaces newlines with %0A
// (the url encoded version of a newline) in the output.
// This is used to format the output for GitHub Actions annotations.
// This is a workaround for the fact that GitHub Actions does not support
// newlines in annotations, so we need to encode them.
type urlEncodeNewlineReader struct {
	rdr *bufio.Reader
}

func (r *urlEncodeNewlineReader) Read(p []byte) (n int, err error) {
	if len(p) == 0 {
		return 0, nil
	}

	peekSize := len(p)
	if peekSize > r.rdr.Size() {
		peekSize = r.rdr.Size()
	}

	peeked, err := r.rdr.Peek(peekSize)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			return 0, fmt.Errorf("failed to peek: %w", err)
		}
		if len(peeked) == 0 {
			return 0, io.EOF
		}
	}

	// Pre-allocate output with the best-case size (same as peeked length)
	output := make([]byte, 0, len(peeked))
	consumed := 0 // Track the number of bytes consumed from peeked

	for i := range peeked {
		if peeked[i] == '\n' {
			output = append(output, '%', '0', 'A')
		} else {
			output = append(output, peeked[i])
		}

		consumed++
		if len(output) >= len(p) {
			break
		}
	}

	// Consume the bytes we processed from the underlying reader
	_, err = r.rdr.Discard(consumed)
	if err != nil {
		return 0, err
	}

	return copy(p, output), nil
}

func getLastFileLine(rdr io.Reader) (file string, line int, retErr error) {
	scanner := bufio.NewScanner(rdr)

	defer func() {
		if retErr == nil {
			return
		}
		slog.Error("failed to get last file and line", "error", retErr, "scanner data", scanner.Text())
	}()

	for scanner.Scan() {
		txt := scanner.Text()
		f, l, ok := getTestOutputLoc(txt)
		if !ok {
			continue
		}

		file = f

		ll, err := strconv.Atoi(l)
		if err != nil {
			slog.Error("failed to parse line number", "line", l, "err", err)
			continue
		}
		line = ll
	}
	return file, line, scanner.Err()
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
