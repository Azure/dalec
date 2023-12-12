package test

import (
	"context"
	"testing"

	"github.com/Azure/dalec/test/testenv"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

func startTestSpan(t *testing.T) context.Context {
	ctx, span := otel.Tracer("").Start(baseCtx, t.Name())
	t.Cleanup(func() {
		if t.Failed() {
			span.SetStatus(codes.Error, "test failed")
		}
		span.End()
	})
	return ctx
}

func runTest(t *testing.T, f gwclient.BuildFunc) {
	ctx := startTestSpan(t)
	testEnv.RunTest(ctx, t, f)
}

// gwClientInputInject is a gwclient.Client that injects the result of a build func into the solve request as an input named by the id.
// This is used to inject a custom frontend into the solve request.
// This does not change what frontend is used, but it does add the custom frontend as an input to the solve request.
// This is so we don't need to have an actual external image from a registry or docker image store.
type gwClientInputInject struct {
	gwclient.Client

	id string
	f  gwclient.BuildFunc
}

func wrapWithInput(c gwclient.Client, id string, f gwclient.BuildFunc) *gwClientInputInject {
	return &gwClientInputInject{
		Client: c,
		id:     id,
		f:      f,
	}
}

func (c *gwClientInputInject) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	res, err := c.f(ctx, c.Client)
	if err != nil {
		return nil, err
	}
	if err := testenv.InjectInput(ctx, res, c.id, &req); err != nil {
		return nil, err
	}
	return c.Client.Solve(ctx, req)
}
