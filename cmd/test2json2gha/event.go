package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/vearutop/dynhist-go"
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
	HandleEvent(ctx context.Context, te *TestEvent) error
}

// statsCollector is an [EventHandler] that collects test statistics
// You can call `WriteSummary` to print the statistics in markdown format
type statsCollector struct {
	slowThreshold time.Duration
	elapsed       map[string]float64
	tests         map[string]struct{}
	skipCount     int
	failCount     int
}

func (h *statsCollector) HandleEvent(_ context.Context, te *TestEvent) error {
	switch te.Action {
	case "fail":
		h.failCount++
	case "skip":
		h.skipCount++
	}

	if h.tests == nil {
		h.tests = make(map[string]struct{})
	}

	key := path.Join(te.Package, te.Test)
	h.tests[key] = struct{}{}

	if te.Elapsed == 0 {
		return nil
	}

	if h.elapsed == nil {
		h.elapsed = make(map[string]float64)
	}
	h.elapsed[key] = te.Elapsed
	return nil
}

func (h *statsCollector) WriteSummary(out io.Writer) {
	hist := &dynhist.Collector{
		PrintSum:     true,
		WeightFunc:   dynhist.ExpWidth(1.2, 0.9),
		BucketsLimit: 10,
	}

	slowBuf := &strings.Builder{}

	var totalTime float64
	for name, elapsed := range h.elapsed {
		hist.Add(elapsed)
		totalTime += elapsed

		if elapsed > h.slowThreshold.Seconds() {
			slowBuf.WriteString(fmt.Sprintf("%s: %.3fs\n", name, elapsed))
		}
	}

	fmt.Fprintln(out, "## Test metrics")
	separator := strings.Repeat("&nbsp;", 4)
	fmt.Fprintln(out, mdBold("Skipped:"), h.skipCount, separator, mdBold("Failed:"), h.failCount, separator, mdBold("Total:"), len(h.tests), separator, mdBold("Elapsed:"), fmt.Sprintf("%.3fs", totalTime))

	fmt.Fprintln(out, mdPreformat(hist.String()))

	if slowBuf.Len() > 0 {
		fmt.Fprintln(out, mdPreformat("## Slow tests"))
		fmt.Fprintln(out, slowBuf.String())
	}
}

func (h *statsCollector) Failed() bool {
	return h.failCount > 0
}

// consoleHandler is an [EventHandler] that writes the test output to the console
// This allows receiving the test output in real time
type consoleHandler struct {
	out io.Writer
}

func (h *consoleHandler) HandleEvent(_ context.Context, te *TestEvent) error {
	if te.Output != "" {
		h.out.Write([]byte(te.Output))
	}
	return nil
}

type resultsHandler struct {
	results map[string]*TestResult
}

func (h *resultsHandler) getOutputStream(te *TestEvent) (*TestResult, error) {
	key := path.Join(te.Package, te.Test)
	tr := h.results[key]
	if tr != nil {
		return tr, nil
	}

	f, err := os.CreateTemp("", strings.Replace(key, "/", "-", -1))
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

func (h *resultsHandler) HandleEvent(ctx context.Context, te *TestEvent) error {
	tr, err := h.getOutputStream(te)
	if err != nil {
		return err
	}

	if te.Action == "fail" {
		tr.failed = true
	}

	if te.Output != "" {
		_, err := tr.output.WriteString(te.Output)
		if err != nil {
			return errors.Wrap(err, "error collecting test event output")
		}
	}

	return nil
}

func (h *resultsHandler) Close() {
	for _, tr := range h.results {
		tr.Close()
	}
}

// TestResult is where we collect all the data about a test
type TestResult struct {
	output *os.File
	failed bool
	pkg    string
	name   string
}

func (r *TestResult) Close() {
	r.output.Close()
}
