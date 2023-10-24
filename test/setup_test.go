package test

import (
	"context"
	"testing"

	"github.com/moby/buildkit/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

var (
	// This gets set in [TestMain]
	// The value of this gets set to a synce.OnceValues which will call the corresponding `_`+`funcName` function.
	pushFrontend                  func() (string, error)
	supportsFrontendNamedContexts func() bool
)

func startTestSpan(ctx context.Context, t *testing.T) context.Context {
	ctx, span := otel.Tracer("").Start(ctx, t.Name())
	t.Cleanup(func() {
		if t.Failed() {
			span.SetStatus(codes.Error, "test failed")
		}
		span.End()
	})
	return ctx
}

type testSolveFunc func(_ context.Context, t *testing.T, _ gwclient.Client, frontendRef string)

func testFrontend(t *testing.T, f testSolveFunc) {
	ctx := startTestSpan(baseCtx, t)

	if supportsFrontendNamedContexts() {
		testWithFrontendNamedContext(ctx, t, baseClient, f)
		return
	}
	testWithFrontendRegistry(ctx, t, baseClient, f)
}

// testWithFrontendNamedContext runs the provided function with the locally built frontend image.
// This is done by piping the output of the frontend build directly to the gateway frontend.
// The neccessary modifications are made to the solve request from the test function to make this happen.
//
// (note: its not actually piping results, just passing around the reference which, when consumed, will be built JIT).
func testWithFrontendNamedContext(ctx context.Context, t *testing.T, c *client.Client, f testSolveFunc) {
	id := identity.NewID()

	eg, ctx := errgroup.WithContext(ctx)
	ch := displaySolveStatus(ctx, eg)

	var so client.SolveOpt
	if err := withProjectRoot(&so); err != nil {
		t.Fatal(err)
	}

	eg.Go(func() error {
		_, err := c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			gwc = &gatewayClientNamedContextFrontend{
				Client: gwc,
				id:     id,
			}
			f(ctx, t, gwc, id)
			return gwclient.NewResult(), nil
		}, ch)
		return err
	})

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

// gatewayClientLocalFrontend is a wrapper around a gateway client which adds the locally built frontend to the solve request.
type gatewayClientNamedContextFrontend struct {
	gwclient.Client
	id string
}

func (c *gatewayClientNamedContextFrontend) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	if err := withLocaFrontendInputs(ctx, c.Client, &req, c.id); err != nil {
		return nil, err
	}
	req.Evaluate = true
	return c.Client.Solve(ctx, req)
}

// testWithFrontendRegistry runs the provided function with the locally built
// frontend image that gets pushed to a local registry.
func testWithFrontendRegistry(ctx context.Context, t *testing.T, c *client.Client, f testSolveFunc) {
	imgName, err := pushFrontend()
	if err != nil {
		t.Fatal(err)
	}

	eg, ctx := errgroup.WithContext(ctx)
	ch := displaySolveStatus(ctx, eg)

	var so client.SolveOpt
	eg.Go(func() error {
		_, err = c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			gwc = &gatewayClientRegistryFrontend{gwc, imgName}
			f(ctx, t, gwc, imgName)
			return gwclient.NewResult(), nil
		}, ch)
		return err
	})

	err = eg.Wait()
	if err != nil {
		t.Fatal(err)
	}
}

// gatewayClientRegistryFrontend is a wrapper around a gateway client which adds
// the frontend image that was built and pushed to the local registry to the
// solve request.
type gatewayClientRegistryFrontend struct {
	gwclient.Client
	ref string
}

func (c *gatewayClientRegistryFrontend) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	req.Frontend = "gateway.v0"
	if req.FrontendOpt == nil {
		req.FrontendOpt = make(map[string]string)
	}
	req.FrontendOpt["source"] = c.ref
	return c.Client.Solve(ctx, req)
}
