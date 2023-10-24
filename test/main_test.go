package test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"testing"
	"time"

	"github.com/moby/buildkit/util/tracing/detect"
	_ "github.com/moby/buildkit/util/tracing/detect/delegated"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

var (
	baseCtx = context.Background()
)

func TestMain(m *testing.M) {
	flag.Parse()

	if v := os.Getenv("OTEL_SERVICE_NAME"); v == "" {
		os.Setenv("OTEL_SERVICE_NAME", "dalec-test")
	}

	// Note: by default we'll use the buildkit "delegated" trace exporter, but if any of these OTLP vars are set it will use the OTLP exporter.
	// "delegated" uses buildkit's own embedded otlp endpoint to send traces, which is more convenient, assuming you've configured buildkit to export traces.
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != "" {
		if os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL") == "" && os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL") == "" {
			// In this case the otlp exporter is configured but the default
			// protocol used by the `detect` package is grpc, but the otel default
			// changed a few versions back and is http/protobuf.
			// So set the default protocol to to http/protobuf so trace exports don't fail.
			os.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "http/protobuf")
		}
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	tp, err := detect.TracerProvider()
	if err != nil {
		panic(err)
	}
	otel.SetTracerProvider(tp)

	ctx, cancel := signal.NotifyContext(baseCtx, os.Interrupt)
	defer cancel()
	baseCtx = ctx

	baseClient, err = defaultBuildkitClient(ctx)
	if err != nil {
		panic(err)
	}

	run := func() int {
		releaseReg := func(context.Context) error { return nil }
		pushFrontend = sync.OnceValues(func() (string, error) {
			var (
				err error
				s   string
			)
			s, releaseReg, err = _pushFrontendToRegistry(ctx, baseClient)
			return s, err
		})

		supportsFrontendNamedContexts = sync.OnceValue(func() bool {
			return _supportsFrontendNamedContexts(ctx, baseClient)
		})
		defer func() {
			if err := releaseReg(context.WithoutCancel(ctx)); err != nil {
				fmt.Fprintln(os.Stderr, "Error releasing registry:", err)
			}
			ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			_ = detect.Shutdown(ctx)
			cancel()
		}()

		return m.Run()
	}

	os.Exit(run())
}
