package main

import (
	"fmt"
	"io"
	"iter"
	"path"
	"strings"
	"time"

	"github.com/vearutop/dynhist-go"
)

type summaryFormatter struct {
	slowThreshold time.Duration
}

func (f *summaryFormatter) FormatResults(results iter.Seq[*TestResult], out io.Writer) error {
	hist := &dynhist.Collector{
		PrintSum:     true,
		WeightFunc:   dynhist.ExpWidth(1.2, 0.9),
		BucketsLimit: 10,
	}

	slowBuf := &strings.Builder{}

	var (
		totalTime    float64
		skipped      int
		failed       int
		totalResults int
	)
	for result := range results {
		hist.Add(result.elapsed)
		totalTime += result.elapsed
		totalResults++

		if result.skipped {
			skipped++
		}
		if result.failed {
			failed++
		}

		if result.elapsed > f.slowThreshold.Seconds() {
			slowBuf.WriteString(fmt.Sprintf("%s: %.3fs\n", path.Join(result.pkg, result.name), result.elapsed))
		}
	}

	fmt.Fprintln(out, "## Test metrics")
	separator := strings.Repeat("&nbsp;", 4)
	fmt.Fprintln(out, mdBold("Skipped:"), skipped, separator, mdBold("Failed:"), failed, separator, mdBold("Total:"), totalResults, separator, mdBold("Elapsed:"), fmt.Sprintf("%.3fs", totalTime))

	fmt.Fprintln(out, mdPreformat(hist.String()))

	if slowBuf.Len() > 0 {
		fmt.Fprintln(out, "## Slow tests")
		fmt.Fprintln(out, mdPreformat(slowBuf.String()))
	}

	return nil
}
