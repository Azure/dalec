package test

import (
	"context"
	"path"
	"sync"
	"testing"

	"github.com/moby/buildkit/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
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

type testSolveConfig struct {
	frontends map[string]gwclient.BuildFunc
}

type testSolveOpt func(*testSolveConfig)

type testSolveFunc func(_ context.Context, t *testing.T, _ gwclient.Client)

func withFrontend(name string, f gwclient.BuildFunc) testSolveOpt {
	return func(c *testSolveConfig) {
		if c.frontends == nil {
			c.frontends = make(map[string]gwclient.BuildFunc)
		}
		c.frontends[name] = f
	}
}

// testWithFrontendNamedContext runs the provided function with the locally built frontend image.
// This is done by piping the output of the frontend build directly to the gateway frontend.
// The neccessary modifications are made to the solve request from the test function to make this happen.
//
// (note: its not actually piping results, just passing around the reference which, when consumed, will be built JIT).
func testWithFrontendNamedContext(ctx context.Context, t *testing.T, c *client.Client, f testSolveFunc, opts ...testSolveOpt) {
	id := identity.NewID()

	eg, ctx := errgroup.WithContext(ctx)
	ch := displaySolveStatus(ctx, eg)

	var so client.SolveOpt
	withProjectRoot(t, &so)

	var cfg testSolveConfig
	for _, o := range opts {
		o(&cfg)
	}

	eg.Go(func() error {
		_, err := c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			gwc = &gatewayClientNamedContextFrontend{
				Client: gwc,
				id:     id,
				cfg:    &cfg,
			}
			f(ctx, t, gwc)
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
	id  string
	cfg *testSolveConfig
}

func (c *gatewayClientNamedContextFrontend) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	if err := withLocaFrontendInputs(ctx, c.Client, &req, c.id); err != nil {
		return nil, err
	}
	for name, f := range c.cfg.frontends {
		if err := injectInput(ctx, c.Client, f, name, &req); err != nil {
			return nil, err
		}
	}
	return c.Client.Solve(ctx, req)
}

// testWithFrontendRegistry runs the provided function with the locally built
// frontend image that gets pushed to a local registry.
// Because this requires the fontend to be exported (either to docker or a registry),
// the frontend build is executed ahead of time rather than just-in-time.
func testWithFrontendRegistry(ctx context.Context, t *testing.T, c *client.Client, f testSolveFunc, opts ...testSolveOpt) {
	imgName, err := buildFrontendImage(ctx, c, t)
	if err != nil {
		t.Fatal(err)
	}

	var cfg testSolveConfig
	for _, o := range opts {
		o(&cfg)
	}

	var sp *spb.Policy
	if len(cfg.frontends) > 0 {
		// Build all the requested frontend images
		// These either get pushed to the local registry or injected directly into dockerd's image store.
		reg := registryHost(ctx, t)

		sp = &spb.Policy{}

		for name, f := range cfg.frontends {
			// Note that the buildkit client closes out the solve status channel when it returns, so each build requires a new client.
			eg, ctx := errgroup.WithContext(ctx)
			ch := displaySolveStatus(ctx, eg)

			img := path.Join(reg, "dalec/test", name)

			eg.Go(func() error {
				var so client.SolveOpt
				setRegistryExport(img, &so)
				withProjectRoot(t, &so)

				_, err := c.Build(ctx, so, "", f, ch)
				return err
			})

			if err := eg.Wait(); err != nil {
				t.Fatal(err)
			}

			// Use a source policy to update the frontend image ref to use the local registry.
			// Note: this is not used to build the frontend, but rather to update the frontend ref for the test function.
			// This allows whatever ref was passed in by the caller to be be rewritten transparently to use the local registry.
			//
			// How this ends up getting used is, in a test we'll see something like `withFrontend("foo", fixtures.FooFrontend)`.
			// In this case the reference passed in here is `foo`.
			// In the test itself, the frontend reference `foo` is used in the [dalec.Spec], like so:
			// dalec.Spec{Targets: map[string]dalec.Target{"whatever": {Frontend: &dalec.Frontend{Image: "foo"}}}}
			// This policy matches against that reference and converts it to the local registry.
			sp.Rules = append(sp.Rules, &spb.Rule{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: "docker-image://" + name,
				},
				Updates: &spb.Update{
					Identifier: "docker-image://" + img,
				},
			})
		}
	}

	var so client.SolveOpt
	so.SourcePolicy = sp
	withProjectRoot(t, &so)

	eg, ctx := errgroup.WithContext(ctx)
	ch := displaySolveStatus(ctx, eg)
	eg.Go(func() error {
		_, err = c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			gwc = &gatewayClientRegistryFrontend{gwc, imgName}
			f(ctx, t, gwc)
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

var (
	supportsFrontendNamedContextsOnce sync.Once
	supportsFrontendNamedContextsVal  bool
)

// testEnv sets up test environment fo rhte provided function.
// It routes the test function to the appropriate test environment based on the capabilities of the buildkit backend.
//
// Buildkit solve requests performed by the testSolveFunc will have the correct frontend image injected into the solve request.
func testEnv(t *testing.T, f testSolveFunc, opts ...testSolveOpt) {
	ctx := startTestSpan(baseCtx, t)

	supportsFrontendNamedContextsOnce.Do(func() {
		supportsFrontendNamedContextsVal = supportsFrontendNamedContexts(ctx, baseClient)
	})

	if supportsFrontendNamedContextsVal {
		testWithFrontendNamedContext(ctx, t, baseClient, f, opts...)
		return
	}
	testWithFrontendRegistry(ctx, t, baseClient, f, opts...)
}
