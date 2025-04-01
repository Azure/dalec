package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type config struct {
	slowThreshold time.Duration
	modName       string
	verbose       bool
	stream        bool
	logDir        string
}

func main() {
	tmp, err := os.MkdirTemp("", "test2json2gha-")
	if err != nil {
		panic(err)
	}
	var cfg config

	flag.DurationVar(&cfg.slowThreshold, "slow", 500*time.Millisecond, "Threshold to mark test as slow")
	flag.BoolVar(&cfg.verbose, "verbose", false, "Enable verbose output")
	flag.BoolVar(&cfg.stream, "stream", false, "Enable streaming output")
	flag.StringVar(&cfg.logDir, "logdir", "", "Directory to store all test logs")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	info, _ := debug.ReadBuildInfo()
	if info != nil {
		cfg.modName = info.Main.Path
	}

	// Set TMPDIR so that [os.CreateTemp] can use an empty string as the dir
	// and wind up in our dir.
	os.Setenv("TMPDIR", tmp)

	cleanup := func() { os.RemoveAll(tmp) }

	anyFail, err := do(os.Stdin, os.Stdout, cfg)
	cleanup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%+v", err)
		os.Exit(1)
	}

	if anyFail {
		// In case pipefail is not enabled, make sure we exit non-zero
		os.Exit(2)
	}
}

func do(in io.Reader, out io.Writer, cfg config) (bool, error) {
	dec := json.NewDecoder(in)

	results := &resultsHandler{}
	defer results.Cleanup()

	defer func() {
		var wg waitGroup

		wg.Go(func() {
			var rf ResultsFormatter
			rf = &consoleFormatter{modName: cfg.modName, verbose: cfg.verbose}
			if err := rf.FormatResults(results.Results(), out); err != nil {
				slog.Error("Error writing annotations", "error", err)
			}

			rf = &errorAnnotationFormatter{}
			if err := rf.FormatResults(results.Results(), out); err != nil {
				slog.Error("Error writing error annotations", "error", err)
			}
		})

		wg.Go(func() {
			summary := getSummaryFile()
			formatter := &summaryFormatter{slowThreshold: cfg.slowThreshold}
			if err := formatter.FormatResults(results.Results(), getSummaryFile()); err != nil {
				slog.Error("Error writing summary", "error", err)
			}
			summary.Close()
		})

		wg.Wait()

		if cfg.logDir != "" {
			results.WriteLogs(cfg.logDir)
		}
	}()

	var anyFailed checkFailed
	handlers := []EventHandler{
		results,
		&anyFailed,
	}

	if cfg.stream {
		handlers = append(handlers, &outputStreamer{out: out})
	}

	te := &TestEvent{}
	for {
		*te = TestEvent{} // Reset the event struct to avoid reusing old data

		err := dec.Decode(te)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, errors.WithStack(err)
		}

		for _, h := range handlers {
			if err := h.HandleEvent(te); err != nil {
				slog.Error("Error handling event", "error", err)
			}
		}
	}

	return bool(anyFailed), nil
}

type waitGroup struct {
	sync.WaitGroup
}

func (wg *waitGroup) Go(f func()) {
	wg.Add(1)
	go func() {
		f()
		wg.Done()
	}()
}
