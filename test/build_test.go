package test

import (
	"context"
	"testing"

	"github.com/Azure/dalec"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

func specToSolveRequest(ctx context.Context, t *testing.T, spec *dalec.Spec, sr *gwclient.SolveRequest) {
	t.Helper()

	dt, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	def, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o644, dt)).Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if sr.FrontendInputs == nil {
		sr.FrontendInputs = make(map[string]*pb.Definition)
	}

	sr.FrontendInputs[dockerui.DefaultLocalNameContext] = def.ToPB()
	sr.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = def.ToPB()
}
