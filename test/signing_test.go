package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/assert"
)

func distroSigningTest(t *testing.T, spec *dalec.Spec, buildTarget string) func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
	return func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		topTgt, _, _ := strings.Cut(buildTarget, "/")

		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(buildTarget))
		res, err := gwc.Solve(ctx, sr)
		if err != nil {
			t.Fatal(err)
		}

		tgt := readFile(ctx, t, "/target", res)
		cfg := readFile(ctx, t, "/config.json", res)

		if string(tgt) != topTgt {
			t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
		}

		if !strings.Contains(string(cfg), "linux") {
			t.Fatal(fmt.Errorf("configuration incorrect"))
		}

		for k, v := range spec.PackageConfig.Signer.Args {
			dt := readFile(ctx, t, "/env/"+k, res)
			assert.Equal(t, v, string(dt))
		}

		return gwclient.NewResult(), nil
	}
}
