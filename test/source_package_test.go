package test

import (
	"context"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

// testSourceOutputBuilds is a regression test for a nil image-config panic in
// the "output-only" targets that emit the package source form (deb "/dsc",
// rpm "/rpm/debug/sources"). The deb handler returned a nil image config, which
// buildkit dereferenced when assembling the export platform. Building the
// target is enough to exercise that path across distros.
func testSourceOutputBuilds(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	var sourceTarget string
	switch {
	case strings.HasSuffix(targetCfg.Package, "/deb"):
		sourceTarget = strings.TrimSuffix(targetCfg.Package, "/deb") + "/dsc"
	case strings.HasSuffix(targetCfg.Package, "/rpm"):
		sourceTarget = targetCfg.Package + "/debug/sources"
	default:
		t.Skipf("no source-output target known for package target %q", targetCfg.Package)
	}

	spec := &dalec.Spec{
		Name:        "test-dalec-source-output",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing source output target builds",
		License:     "MIT",
		Sources: map[string]dalec.Source{
			"src": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{Contents: "hello world"},
				},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"src": {},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{Command: "true"},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(sourceTarget))
		solveT(ctx, t, gwc, sr)
	})
}
