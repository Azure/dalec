package main

import (
	"os"
	"time"

	"github.com/pkg/errors"
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

// TestResult is where we collect all the data about a test
type TestResult struct {
	output  *os.File
	failed  bool
	pkg     string
	name    string
	elapsed float64
	skipped bool
}

func (r *TestResult) Close() {
	r.output.Close()
}

func collectTestOutput(te *TestEvent, tr *TestResult) error {
	if te.Output != "" {
		_, err := tr.output.Write([]byte(te.Output))
		if err != nil {
			return errors.Wrap(err, "error collecting test event output")
		}
	}

	tr.pkg = te.Package
	tr.name = te.Test
	if te.Elapsed > 0 {
		tr.elapsed = te.Elapsed
	}

	if te.Action == "fail" {
		tr.failed = true
	}
	if te.Action == "skip" {
		tr.skipped = true
	}
	return nil
}
