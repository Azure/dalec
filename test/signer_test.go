package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// TestHandlerTargetForwarding tests that targets are forwarded to the correct frontend.
// We do this by registering a phony frontend and then forwarding a target to it and checking the outputs.
func TestSignerForwarding(t *testing.T) {
	runTest := func(t *testing.T, f gwclient.BuildFunc) {
		t.Helper()
		ctx := startTestSpan(baseCtx, t)
		testEnv.RunTest(ctx, t, f)
	}

	t.Run("test mariner2 signing", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			spec := dalec.Spec{
				Name:        "foo",
				Version:     "v0.0.1",
				Description: "foo bar baz",
				Website:     "https://foo.bar.baz",
				Revision:    "1",
				Targets: map[string]dalec.Target{
					"mariner2": {
						PackageConfig: &dalec.PackageConfig{
							Signer: &dalec.Frontend{
								Image: phonySignerRef,
							},
						},
					},
				},
				Sources: map[string]dalec.Source{
					"foo": {
						Inline: &dalec.SourceInline{
							File: &dalec.SourceInlineFile{
								Contents: "#!/usr/bin/env bash\necho \"hello, world!\"\n",
							},
						},
					},
				},
				Build: dalec.ArtifactBuild{
					Steps: []dalec.BuildStep{
						{
							Command: "/bin/true",
						},
					},
				},
				Artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"foo": {},
					},
				},
			}

			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget("mariner2/rpm"))
			res, err := gwc.Solve(ctx, sr)
			if err != nil {
				t.Fatal(err)
			}

			tgt := readFile(ctx, t, "/target", res)
			cfg := readFile(ctx, t, "/config.json", res)

			if string(tgt) != "mariner2" {
				t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
			}

			if !strings.Contains(string(cfg), "LinuxSign") {
				t.Fatal(fmt.Errorf("configuration incorrect"))
			}

			return gwclient.NewResult(), nil
		})
	})

	t.Run("test windows signing", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			spec := fillMetadata("foo", &dalec.Spec{
				Targets: map[string]dalec.Target{
					"windowscross": {
						PackageConfig: &dalec.PackageConfig{
							Signer: &dalec.Frontend{
								Image: phonySignerRef,
							},
						},
					},
				},
				Sources: map[string]dalec.Source{
					"foo": {
						Inline: &dalec.SourceInline{
							File: &dalec.SourceInlineFile{
								Contents: "#!/usr/bin/env bash\necho \"hello, world!\"\n",
							},
						},
					},
				},
				Build: dalec.ArtifactBuild{
					Steps: []dalec.BuildStep{
						{
							Command: "/bin/true",
						},
					},
				},
				Artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"foo": {},
					},
				},
			})

			zipperSpec := fillMetadata("bar", &dalec.Spec{
				Dependencies: &dalec.PackageDependencies{
					Runtime: map[string][]string{
						"unzip": {},
					},
				},
			})

			sr := newSolveRequest(withSpec(ctx, t, zipperSpec), withBuildTarget("mariner2/container"))
			zipper := reqToState(ctx, gwc, sr, t)

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget("windowscross/zip"))
			st := reqToState(ctx, gwc, sr, t)

			st = zipper.Run(llb.Args([]string{"bash", "-c", `for f in ./*.zip; do unzip "$f"; done`}), llb.Dir("/tmp/mnt")).
				AddMount("/tmp/mnt", st)

			def, err := st.Marshal(ctx)
			if err != nil {
				t.Fatal(err)
			}

			res, err := gwc.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				t.Fatal(err)
			}

			tgt := readFile(ctx, t, "/target", res)
			cfg := readFile(ctx, t, "/config.json", res)

			if string(tgt) != "windowscross" {
				t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
			}

			if !strings.Contains(string(cfg), "NotaryCoseSign") {
				t.Fatal(fmt.Errorf("configuration incorrect"))
			}

			return gwclient.NewResult(), nil
		})
	})
}

func reqToState(ctx context.Context, gwc gwclient.Client, sr gwclient.SolveRequest, t *testing.T) llb.State {
	res, err := gwc.Solve(ctx, sr)
	if err != nil {
		t.Fatal(err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	st, err := ref.ToState()
	if err != nil {
		t.Fatal(err)
	}

	return st
}

func fillMetadata(fakename string, s *dalec.Spec) *dalec.Spec {
	s.Name = "bar"
	s.Version = "v0.0.1"
	s.Description = "foo bar baz"
	s.Website = "https://foo.bar.baz"
	s.Revision = "1"
	s.License = "MIT"
	s.Vendor = "nothing"
	s.Packager = "Bill Spummins"

	return s
}
