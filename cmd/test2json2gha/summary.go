package main

import (
	"bytes"
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
			fmt.Fprintf(slowBuf, "%s: %.3fs\n", path.Join(result.pkg, result.name), result.elapsed)
		}
	}

	buf := bytes.NewBuffer(nil)
	fmt.Fprintln(buf, "## Test metrics")
	separator := strings.Repeat("&nbsp;", 4)
	fmt.Fprintln(buf, mdBold("Skipped:"), skipped, separator, mdBold("Failed:"), failed, separator, mdBold("Total:"), totalResults, separator, mdBold("Elapsed:"), fmt.Sprintf("%.3fs", totalTime))

	fmt.Fprintln(buf, mdPreformat(hist.String()))

	if slowBuf.Len() > 0 {
		fmt.Fprintln(buf, "## Slow tests")
		fmt.Fprintln(buf, mdPreformat(slowBuf.String()))
	}

	_, err := io.Copy(out, buf)

	return err
}
