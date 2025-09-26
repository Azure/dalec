package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

const (
	testModuleName = "some/module/name"
)

func TestDo(t *testing.T) {
	passGroup := "::group::some_package.TestGenPass\n" + testEventPassOutput + "::endgroup::\n"
	failGroup := "::group::some_package.TestGenFail\n" + testEventFailOutput + "::endgroup::\n"
	timeoutGroup := "::group::some_package.TestGenTimeout\n::warning::Test timed out\n" + testEventTimeoutOutput + "::endgroup::\n"
	skipGroup := "::group::some_package.TestGenSkip\n" + testEventSkipOutput + "::endgroup::\n"

	timeoutAnnotation := strings.TrimPrefix(testEventTimeoutOutput, "=== RUN   TestGenTimeout\n")
	annotation := "::error file=foo_test.go,line=43::" + strings.ReplaceAll(testLogsAnnotation, "\n", "%0A") + "\n" + "::error file=foo_test.go,line=9::" + strings.ReplaceAll(timeoutAnnotation, "\n", "%0A") + "\n"

	t.Run("verbose=false", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		ghaBuff := bytes.NewBuffer(nil)
		cfg := config{
			slowThreshold:  200 * time.Millisecond,
			modName:        testModuleName,
			verbose:        false,
			stream:         false,
			ghaCommandsOut: ghaBuff,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// Non-verbose output should only include the grouped fail events + error annotations
		expected := failGroup + timeoutGroup + annotation
		assert.Check(t, cmp.Equal(expected, output.String()))
		assert.Check(t, cmp.Equal(ghaCommandsOutput, ghaBuff.String()))
	})

	t.Run("verbose=true", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		ghaBuff := bytes.NewBuffer(nil)
		cfg := config{
			slowThreshold:  200 * time.Millisecond,
			modName:        testModuleName,
			verbose:        true,
			stream:         false,
			ghaCommandsOut: ghaBuff,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// verbose output should include grouped events for all test results + error annotations
		expected := failGroup + passGroup + skipGroup + timeoutGroup + annotation
		assert.Equal(t, output.String(), expected)
		assert.Check(t, cmp.Equal(expected, output.String()))
		assert.Check(t, cmp.Equal(ghaCommandsOutput, ghaBuff.String()))
	})

	t.Run("stream=true", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		ghaBuff := bytes.NewBuffer(nil)
		cfg := config{
			slowThreshold:  200 * time.Millisecond,
			modName:        testModuleName,
			verbose:        false,
			stream:         true,
			ghaCommandsOut: ghaBuff,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// Stream output should include all the raw events + the grouped fail events + the annotation
		expected := testEventPassOutput + testEventFailOutput + testEventSkipOutput + testEventTimeoutOutput + testEventPackageOutput + failGroup + timeoutGroup + annotation
		assert.Check(t, cmp.Equal(expected, output.String()))
		assert.Check(t, cmp.Equal(ghaCommandsOutput, ghaBuff.String()))
	})

	t.Run("summary", func(t *testing.T) {
		dir := t.TempDir()
		f, err := os.CreateTemp(dir, "summary")
		assert.NilError(t, err)
		defer f.Close()

		t.Setenv("GITHUB_STEP_SUMMARY", f.Name())

		input := strings.NewReader(testEventJSON)

		ghaBuff := bytes.NewBuffer(nil)
		cfg := config{
			slowThreshold:  200 * time.Millisecond,
			modName:        testModuleName,
			verbose:        false,
			stream:         false,
			ghaCommandsOut: ghaBuff,
		}

		anyFail, err := do(input, io.Discard, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		output, err := os.ReadFile(f.Name())
		assert.NilError(t, err)

		expect := `## Test metrics
**Skipped:** 1 &nbsp;&nbsp;&nbsp;&nbsp; **Failed:** 2 &nbsp;&nbsp;&nbsp;&nbsp; **Total:** 5 &nbsp;&nbsp;&nbsp;&nbsp; **Elapsed:** 0.250s

` + "```" + `
[ min  max] cnt total%  sum (5 events)
[0.00 0.00] 4 80.00% 0.00 ................................................................................
[0.25 0.25] 1 20.00% 0.25 ....................
` + "```" + `

## Slow tests

` + "```" + `
some_package: 0.250s
` + "```" + `

`

		assert.Check(t, cmp.Equal(string(output), expect))
		assert.Check(t, cmp.Equal(ghaCommandsOutput, ghaBuff.String()))
	})

	t.Run("LogDir", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		logDir := t.TempDir()
		ghaBuff := bytes.NewBuffer(nil)
		cfg := config{
			slowThreshold:  500,
			modName:        "github.com/Azure/dalec/cmd/test2json2gha",
			verbose:        false,
			stream:         false,
			logDir:         logDir,
			ghaCommandsOut: ghaBuff,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// Validate that logs are written to the specified directory
		entries, err := os.ReadDir(logDir)
		assert.NilError(t, err)
		assert.Check(t, len(entries) > 0, "expected log files to be written to the log directory")
		assert.Check(t, cmp.Equal(ghaCommandsOutput, ghaBuff.String()))
	})
}
