package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

const (
	testEventJSON = `
{"Time":"2025-03-31T09:46:20.078814-07:00","Action":"start","Package":"some_package"}
{"Time":"2025-03-31T09:46:20.327453-07:00","Action":"run","Package":"some_package","Test":"TestGenPass"}
{"Time":"2025-03-31T09:46:20.327531-07:00","Action":"output","Package":"some_package","Test":"TestGenPass","Output":"=== RUN   TestGenPass\n"}
{"Time":"2025-03-31T09:46:20.327583-07:00","Action":"output","Package":"some_package","Test":"TestGenPass","Output":"    foo_test.go:38: some log\n"}
{"Time":"2025-03-31T09:46:20.32762-07:00","Action":"output","Package":"some_package","Test":"TestGenPass","Output":"--- PASS: TestGenPass (0.00s)\n"}
{"Time":"2025-03-31T09:46:20.327654-07:00","Action":"pass","Package":"some_package","Test":"TestGenPass","Elapsed":0}
{"Time":"2025-03-31T09:46:20.327706-07:00","Action":"run","Package":"some_package","Test":"TestGenFail"}
{"Time":"2025-03-31T09:46:20.327713-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"=== RUN   TestGenFail\n"}
{"Time":"2025-03-31T09:46:20.327757-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"    foo_test.go:42: some error\n"}
{"Time":"2025-03-31T09:46:20.327773-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"    foo_test.go:43: some fatal error\n"}
{"Time":"2025-03-31T09:46:20.327918-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"--- FAIL: TestGenFail (0.00s)\n"}
{"Time":"2025-03-31T09:46:20.327934-07:00","Action":"fail","Package":"some_package","Test":"TestGenFail","Elapsed":0}
{"Time":"2025-03-31T09:46:20.327943-07:00","Action":"run","Package":"some_package","Test":"TestGenSkip"}
{"Time":"2025-03-31T09:46:20.327948-07:00","Action":"output","Package":"some_package","Test":"TestGenSkip","Output":"=== RUN   TestGenSkip\n"}
{"Time":"2025-03-31T09:46:20.327954-07:00","Action":"output","Package":"some_package","Test":"TestGenSkip","Output":"    foo_test.go:47: some skip reason\n"}
{"Time":"2025-03-31T09:46:20.327971-07:00","Action":"output","Package":"some_package","Test":"TestGenSkip","Output":"--- SKIP: TestGenSkip (0.00s)\n"}
{"Time":"2025-03-31T09:46:20.327977-07:00","Action":"skip","Package":"some_package","Test":"TestGenSkip","Elapsed":0}
{"Time":"2025-03-31T09:46:20.328007-07:00","Action":"output","Package":"some_package","Output":"FAIL\n"}
{"Time":"2025-03-31T09:46:20.328475-07:00","Action":"output","Package":"some_package","Output":"FAIL\tsome_package\t0.249s\n"}
{"Time":"2025-03-31T09:46:20.32851-07:00","Action":"fail","Package":"some_package","Elapsed":0.25}
`

	testEventPassOutput    = "=== RUN   TestGenPass\n    foo_test.go:38: some log\n--- PASS: TestGenPass (0.00s)\n"
	testEventFailOutput    = "=== RUN   TestGenFail\n    foo_test.go:42: some error\n    foo_test.go:43: some fatal error\n--- FAIL: TestGenFail (0.00s)\n"
	testEventSkipOutput    = "=== RUN   TestGenSkip\n    foo_test.go:47: some skip reason\n--- SKIP: TestGenSkip (0.00s)\n"
	testEventPackageOutput = "FAIL\nFAIL\tsome_package\t0.249s\n"

	testPackageName = "some_package"
)

func mockTestResults(t *testing.T) iter.Seq[*TestResult] {
	h := &resultsHandler{results: make(map[string]*TestResult)}
	t.Cleanup(func() {
		h.Close()
	})

	return h.Results()
}

func readTestEvents(t *testing.T) iter.Seq[*TestEvent] {
	dec := json.NewDecoder(strings.NewReader(testEventJSON))
	te := &TestEvent{}

	return func(yield func(*TestEvent) bool) {
		t.Helper()

		for {
			*te = TestEvent{}
			err := dec.Decode(te)
			if errors.Is(err, io.EOF) {
				break
			}
			assert.NilError(t, err)
			if !yield(te) {
				break
			}
		}
	}
}

func TestOutputStreamer(t *testing.T) {

	var output strings.Builder
	streamer := &outputStreamer{out: &output}

	for event := range readTestEvents(t) {
		err := streamer.HandleEvent(event)
		assert.NilError(t, err)
	}

	expectedOutput := testEventPassOutput + testEventFailOutput + testEventSkipOutput + testEventPackageOutput
	assert.Equal(t, output.String(), expectedOutput)
}

func TestOutputStreamer_HandleEvent(t *testing.T) {
	var output strings.Builder
	streamer := &outputStreamer{out: &output}

	for event := range readTestEvents(t) {
		err := streamer.HandleEvent(event)
		assert.NilError(t, err)
	}

	expectedOutput := testEventPassOutput + testEventFailOutput + testEventSkipOutput + testEventPackageOutput
	assert.Equal(t, output.String(), expectedOutput)
}

func TestResultsHandler(t *testing.T) {
	// Validate specific results
	for r := range mockTestResults(t) {
		assert.Check(t, cmp.Equal(r.pkg, testPackageName))
		switch r.name {
		case "TestGenPass":
			assert.Check(t, !r.failed)
			assert.Check(t, !r.skipped)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventPassOutput))
		case "TestGenFail":
			assert.Check(t, r.failed)
			assert.Check(t, !r.skipped)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventFailOutput))
		case "TestGenSkip":
			assert.Check(t, !r.failed)
			assert.Check(t, r.skipped)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventSkipOutput))
		case "":
			assert.Check(t, r.failed)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventPackageOutput))
		default:
			t.Fatalf("unexpected test result: %s", r.name)
		}
	}
}

func TestCheckFailed(t *testing.T) {
	var failed checkFailed

	for event := range readTestEvents(t) {
		err := failed.HandleEvent(event)
		assert.NilError(t, err)
	}

	assert.Assert(t, bool(failed), "expected checkFailed to be true after a fail event")

	t.Run("HandleMultipleEvents_NoFailures", func(t *testing.T) {
		var failed checkFailed
		events := []*TestEvent{
			{Action: "pass", Package: "mypackage", Test: "Test1", Output: "file1.go:10: Test1 passed\n"},
			{Action: "skip", Package: "mypackage", Test: "Test3", Output: "file3.go:30: Test3 skipped\n"},
		}

		for _, event := range events {
			err := failed.HandleEvent(event)
			assert.NilError(t, err)
		}

		assert.Assert(t, !bool(failed), "expected checkFailed to be false when no fail events are present")
	})
}
