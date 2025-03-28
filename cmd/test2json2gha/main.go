package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"github.com/pkg/errors"
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

	anyFail, err := do(context.TODO(), os.Stdin, os.Stdout, mod, slowThreshold)
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

func do(ctx context.Context, in io.Reader, out io.Writer, modName string, slowThreshold time.Duration) (bool, error) {
	dec := json.NewDecoder(in)

	te := &TestEvent{}

	stats := &statsCollector{
		slowThreshold: slowThreshold,
	}

	defer func() {
		summary := getSummaryFile()
		stats.WriteSummary(summary)
		summary.Close()
	}()

	results := &resultsHandler{}
	defer func() {
		if err := results.WriteAnnotations(modName, out); err != nil {
			slog.Error("Error writing annotations", "error", err)
		}
		results.Close()
	}()

	handlers := []EventHandler{
		&consoleHandler{out: out},
		stats,
		results,
	}

	for {
		err := dec.Decode(te)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, errors.WithStack(err)
		}

		for _, h := range handlers {
			if err := h.HandleEvent(ctx, te); err != nil {
				slog.Error("Error handling event", "error", err)
			}
		}
	}

	return stats.Failed(), nil
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
