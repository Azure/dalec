package main

import (
	"io"
	"iter"
	"math"
	"os"
	"path"
	"strings"
	"time"

	"github.com/Azure/dalec"
	"github.com/pkg/errors"
)

const (
	pass = "pass"
	fail = "fail"
	skip = "skip"
)

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

type EventHandler interface {
	HandleEvent(te *TestEvent) error
}

type ResultsFormatter interface {
	FormatResults(results iter.Seq[*TestResult], out io.Writer) error
}

// outputStreamer is an [EventHandler] that writes the test output to the console
// This allows receiving the test output in real time
type outputStreamer struct {
	out io.Writer
}

func (h *outputStreamer) HandleEvent(te *TestEvent) error {
	if te.Output != "" {
		_, err := h.out.Write([]byte(te.Output))
		return err
	}
	return nil
}

// resultsHandler is an [EventHandler] that gathers all the results from every event
// handled.
type resultsHandler struct {
	results map[string]*TestResult
}

func (h *resultsHandler) getOutputStream(te *TestEvent) (*TestResult, error) {
	key := path.Join(te.Package, te.Test)
	tr := h.results[key]
	if tr != nil {
		return tr, nil
	}

	f, err := os.CreateTemp("", strings.ReplaceAll(key, "/", "-"))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	tr = &TestResult{output: f, pkg: te.Package, name: te.Test}
	if h.results == nil {
		h.results = make(map[string]*TestResult)
	}
	h.results[key] = tr
	return tr, nil
}

func (h *resultsHandler) HandleEvent(te *TestEvent) error {
	tr, err := h.getOutputStream(te)
	if err != nil {
		return err
	}

	switch te.Action {
	case fail:
		tr.failed = true
	case skip:
		tr.skipped = true
	}

	tr.elapsed = te.Elapsed

	if te.Output != "" {
		_, err := tr.output.WriteString(te.Output)
		if err != nil {
			return errors.Wrap(err, "error collecting test event output")
		}
	}

	return nil
}

func (h *resultsHandler) Results() iter.Seq[*TestResult] {
	keys := dalec.SortMapKeys(h.results)
	return func(yield func(*TestResult) bool) {
		for _, key := range keys {
			tr := h.results[key]
			if !yield(tr) {
				break
			}
		}
	}
}

func (h *resultsHandler) Close() {
	for _, tr := range h.results {
		tr.Close()
	}
}

// TestResult is where we collect all the data about a test
type TestResult struct {
	output  *os.File
	pkg     string
	name    string
	failed  bool
	skipped bool
	elapsed float64
}

// [Close] closes the underlying output file and invalidates any readers
// created from [TestResult.Reader].
func (r *TestResult) Close() {
	r.output.Close()
}

// Reader creates a new reader that contains all the test output
// Calling [TestResult.Reader] multiple times will return a new, independent reader
// each time.
//
// Calling [TestResult.Close] will close the underlying file, any readers created before or
// after will be invalid after that and should return an [io.EOF] error on read.
func (r *TestResult) Reader() io.Reader {
	return io.NewSectionReader(r.output, 0, math.MaxInt64)
}

// checkFailed is an [EventHandler] that checks if any test has failed
// and sets the [checkFailed] to true if so.
type checkFailed bool

func (c *checkFailed) HandleEvent(te *TestEvent) error {
	if te.Action == "fail" {
		*c = true
	}
	return nil
}
