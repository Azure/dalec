package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/testenv"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/assert"
)

func distroSigningTest(t *testing.T, spec *dalec.Spec, buildTarget string) testenv.TestFunc {
	return func(ctx context.Context, gwc gwclient.Client) {
		topTgt, _, _ := strings.Cut(buildTarget, "/")

		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(buildTarget))
		res := solveT(ctx, t, gwc, sr)

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
	}
}
