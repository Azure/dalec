package test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/Azure/dalec/test/fixtures"
	"github.com/Azure/dalec/test/testenv"
	"github.com/moby/buildkit/util/tracing/detect"
	_ "github.com/moby/buildkit/util/tracing/detect/delegated"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

var (
	baseCtx          = context.Background()
	testEnv          *testenv.BuildxEnv
	externalTestHost = os.Getenv("TEST_DALEC_EXTERNAL_HOST")
)

func TestMain(m *testing.M) {
	if externalTestHost == "" {
		externalTestHost = "https://github.com"
	}
	flag.StringVar(&externalTestHost, "external-test-host", externalTestHost, "http server to use for validating network access")

	flag.Parse()

	if testing.Short() {
		return
	}

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

	testEnv = testenv.New()

	run := func() int {
		ctx, done := signal.NotifyContext(baseCtx, os.Interrupt)
		baseCtx = ctx

		go func() {
			<-ctx.Done()
			// The context was cancelled due to interupt
			// This _should_ trigger builds to cancel naturally and exit the program,
			// but in some cases it may not (due to timing, bugs in buildkit, uninteruptable operations, etc.).
			// Cancel our signal handler so the normal handler takes over from here.
			// This allows subsequent interupts to use the default behavior (exit the program)
			done()

			<-time.After(30 * time.Second)
			fmt.Fprintln(os.Stderr, "Timeout waiting for builds to cancel after interupt")
			os.Exit(int(syscall.SIGINT))
		}()

		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if err := detect.Shutdown(ctx); err != nil {
				fmt.Fprintln(os.Stderr, "error shutting down tracer:", err)
			}
			cancel()
		}()

		if err := testEnv.Load(ctx, phonyRef, fixtures.PhonyFrontend); err != nil {
			panic(err)
		}

		if err := testEnv.Load(ctx, phonySignerRef, fixtures.PhonySigner); err != nil {
			panic(err)
		}

		return m.Run()
	}

	os.Exit(run())
}
