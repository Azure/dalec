package frontend

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

func TestBuildMux(t *testing.T) {
	ctx := context.Background()

	var mux BuildMux

	newCallback := func() (count func() int, bf gwclient.BuildFunc) {
		var i int

		count = func() int {
			return i
		}
		bf = stubHandler(func() {
			i++
		})
		return count, bf
	}

	// Create a "real" handler that is expected to do things and not be a sub-router.
	realCount, realH := newCallback()
	mux.Add("real", realH, &targets.Target{
		Name:    "real",
		Default: true,
	}) // add a targets.Target because its expected for "real" handlers (ie, not subrouters).

	expectedRealcount := 0
	client := newStubClient(withStubOptTarget("real"))
	_, err := mux.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	expectedRealcount++

	if count := realCount(); count != expectedRealcount {
		t.Errorf("expected real handler call count to be %d, got %d", expectedRealcount, count)
	}

	// create a subrouter namespaced under the "real" handler.
	// This should handle routes for real/subroute/*.
	var subRouter BuildMux
	subRouteACount, subrouteAH := newCallback()
	subRouter.Add("a", subrouteAH, &targets.Target{Name: "a"})
	mux.Add("real/subroute", subRouter.Handle, nil)
	expectedSubrouteACount := 0

	// Run with the same target again
	// This should increase the count of the real handler, not the subrouter.
	_, err = mux.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}

	expectedRealcount++
	if count := realCount(); count != expectedRealcount {
		t.Fatalf("expected real handler to be called %d times, got %d", expectedRealcount, count)
	}

	if count := subRouteACount(); count != expectedSubrouteACount {
		t.Errorf("expected debug/a handler to be called %d times, got %d", expectedSubrouteACount, count)
	}

	client = newStubClient(withStubOptTarget("real/subroute/a"))
	_, err = mux.Handle(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	expectedSubrouteACount++

	// Count should not have changed from above
	if count := realCount(); count != 2 {
		t.Errorf("expected real handler to be called twice, got %d", count)
	}

	if count := subRouteACount(); count != expectedSubrouteACount {
		t.Errorf("expected debug/a handler to be called %d times, got %d", expectedSubrouteACount, count)
	}

}

func stubHandler(cb func()) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		cb()
		return nil, nil
	}
}

var _ gwclient.Client = (*stubClient)(nil)

type stubClient struct {
	opts     map[string]string
	inputs   map[string]llb.State
	imageRes llb.ImageMetaResolver
	metaRes  sourceresolver.MetaResolver
}

type stubOpt func(*stubClient)

func withStubOptTarget(t string) stubOpt {
	return func(c *stubClient) {
		c.opts[keyTarget] = t
	}
}

func newStubClient(opts ...stubOpt) *stubClient {
	c := &stubClient{
		opts:   make(map[string]string),
		inputs: make(map[string]llb.State),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *stubClient) BuildOpts() gwclient.BuildOpts {
	return gwclient.BuildOpts{
		Opts: maps.Clone(c.opts),
	}
}

func (c *stubClient) Inputs(context.Context) (map[string]llb.State, error) {
	return maps.Clone(c.inputs), nil
}

func (c *stubClient) NewContainer(context.Context, gwclient.NewContainerRequest) (gwclient.Container, error) {
	return nil, errors.New("not implemented")
}

func (c *stubClient) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	if c.imageRes == nil {
		return "", "", nil, errors.New("not implemented")
	}
	return c.imageRes.ResolveImageConfig(ctx, ref, opt)
}

func (c *stubClient) ResolveSourceMetadata(ctx context.Context, op *pb.SourceOp, opt sourceresolver.Opt) (*sourceresolver.MetaResponse, error) {
	if c.metaRes == nil {
		return nil, errors.New("not implemented")
	}
	return c.metaRes.ResolveSourceMetadata(ctx, op, opt)
}

func (c *stubClient) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	return nil, errors.New("not implemented")
}

func (c *stubClient) Warn(ctx context.Context, dgst digest.Digest, msg string, opts gwclient.WarnOpts) error {
	return errors.New("not implemented")
}
