package test

import (
	"context"
	"log"
	"testing"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/goccy/go-yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func getBuildPlatform(ctx context.Context, t *testing.T) *platforms.Platform {
	buildPlatform := make(chan *platforms.Platform, 1)
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		defer close(buildPlatform)
		dc, err := dockerui.NewClient(gwc)
		if err != nil {
			t.Fatal(err)
		}
		if len(dc.BuildPlatforms) > 1 {
			t.Fatal("unexpected build platforms")
		}
		buildPlatform <- &dc.BuildPlatforms[0]
		return gwclient.NewResult(), nil
	})

	p := <-buildPlatform

	if p == nil {
		t.Fatal("build platform not found")
	}

	return p
}

func Test_PassthroughVars(t *testing.T) {
	runTest := func(t *testing.T, f gwclient.BuildFunc) {
		t.Helper()
		ctx := startTestSpan(t)
		testEnv.RunTest(ctx, t, f)
	}

	var buildPlatform = getBuildPlatform(startTestSpan(t), t)
	log.Println(platforms.Format(*buildPlatform))

	tests := []struct {
		name               string
		needsBuildPlatform bool
		targetPlatform     platforms.Platform
		buildPlatform      *platforms.Platform
		optInArgs          map[string]string
		env                map[string]string
		wantEnv            map[string]string
	}{
		{
			name:           "target linux/amd64",
			targetPlatform: platforms.MustParse("linux/amd64"),
			optInArgs: map[string]string{
				"TARGETOS":       "",
				"TARGETARCH":     "",
				"TARGETVARIANT":  "",
				"TARGETPLATFORM": "",
			},
			env: map[string]string{
				"TARGETOS":       "${TARGETOS}",
				"TARGETARCH":     "${TARGETARCH}",
				"TARGETVARIANT":  "${TARGETVARIANT}",
				"TARGETPLATFORM": "${TARGETPLATFORM}",
			},
			wantEnv: map[string]string{
				"TARGETOS":       "linux",
				"TARGETARCH":     "amd64",
				"TARGETVARIANT":  "",
				"TARGETPLATFORM": "linux/amd64",
			},
		},
		{
			name:           "target linux/arm/v5",
			targetPlatform: platforms.MustParse("linux/arm/v5"),
			optInArgs: map[string]string{
				"TARGETOS":       "",
				"TARGETARCH":     "",
				"TARGETVARIANT":  "",
				"TARGETPLATFORM": "",
			},
			env: map[string]string{
				"TARGETOS":       "${TARGETOS}",
				"TARGETARCH":     "${TARGETARCH}",
				"TARGETVARIANT":  "${TARGETVARIANT}",
				"TARGETPLATFORM": "${TARGETPLATFORM}",
			},
			wantEnv: map[string]string{
				"TARGETOS":       "linux",
				"TARGETARCH":     "arm",
				"TARGETVARIANT":  "v5",
				"TARGETPLATFORM": "linux/arm/v5",
			},
		},
		{
			name:           "build platform",
			targetPlatform: platforms.DefaultSpec(),
			optInArgs: map[string]string{
				"BUILDOS":       "",
				"BUILDARCH":     "",
				"BUILDVARIANT":  "",
				"BUILDPLATFORM": "",
			},
			env: map[string]string{
				"BUILDOS":       "${BUILDOS}",
				"BUILDARCH":     "${BUILDARCH}",
				"BUILDVARIANT":  "${BUILDVARIANT}",
				"BUILDPLATFORM": "${BUILDPLATFORM}",
			},
			wantEnv: map[string]string{
				"BUILDOS":       buildPlatform.OS,
				"BUILDARCH":     buildPlatform.Architecture,
				"BUILDVARIANT":  buildPlatform.Variant,
				"BUILDPLATFORM": platforms.Format(*buildPlatform),
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
				spec := &dalec.Spec{Args: tt.optInArgs, Build: dalec.ArtifactBuild{Env: tt.env}}
				req := newSolveRequest(withBuildTarget("debug/resolve"), withSpec(ctx, t, spec), withPlatform(ctx, t, tt.targetPlatform))
				res, err := gwc.Solve(ctx, req)
				if err != nil {
					return nil, err
				}

				specBytes := readFile(ctx, t, "spec.yml", res)

				var resolvedSpec dalec.Spec
				yaml.Unmarshal(specBytes, &resolvedSpec)
				if err != nil {
					return nil, err
				}

				if !cmp.Equal(tt.wantEnv, resolvedSpec.Build.Env) {
					t.Fatalf("resolved environment does not match: %v", cmp.Diff(tt.wantEnv, resolvedSpec.Build.Env))
				}

				return gwclient.NewResult(), nil
			})
		})

	}
}
