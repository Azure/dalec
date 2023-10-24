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

// TestHandlerTargetForwarding tests that the frontend forwards the build to the specified frontend image.
// In the case of this test we are forwarding to the same frontend image, but with a custom build target name.
// This should result in there being target with a prefix "foo", e.g. `"foo/debug/resolve"` instead of any
// of the built-in targets and forwards the build to the frontend image.
// Note: Without instrumenting the frontend with special things for testing, it is difficult to tell if
// the target was actually forwarded, but we can check that the target actually works.
func TestHandlerTargetForwarding(t *testing.T) {
	const expectedFOO = "hello"
	doTest := func(ct *testing.T, target string) {
		t.Run("target="+target, func(t *testing.T) {
			t.Parallel()

			testFrontend(t, func(ctx context.Context, t *testing.T, gwc gwclient.Client, frontendID string) {
				spec := &dalec.Spec{
					Args: map[string]string{
						"FOO": "",
					},
					Version: "${FOO}",
					Targets: map[string]dalec.Target{
						"foo": {
							Frontend: &dalec.Frontend{
								Image: frontendID,
							},
						},
						"mariner2": {
							Frontend: &dalec.Frontend{
								Image: frontendID,
							},
						},
					},
				}

				dt, err := yaml.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}

				dfSt, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o644, dt)).Marshal(ctx)
				if err != nil {
					t.Fatal(err)
				}

				opt := gwclient.SolveRequest{
					FrontendOpt: map[string]string{
						"build-arg:FOO": expectedFOO,
						"target":        target,
					},
					FrontendInputs: map[string]*pb.Definition{
						dockerui.DefaultLocalNameContext:    dfSt.ToPB(),
						dockerui.DefaultLocalNameDockerfile: dfSt.ToPB(),
					},
				}

				res, err := gwc.Solve(ctx, opt)
				if err != nil {
					t.Fatal(err)
				}

				ref, err := res.SingleRef()
				if err != nil {
					t.Fatal(err)
				}

				dt, err = ref.ReadFile(ctx, gwclient.ReadRequest{Filename: "spec.yml"})
				if err != nil {
					t.Fatal(err)
				}

				var got dalec.Spec
				if err := yaml.Unmarshal(dt, &got); err != nil {
					t.Fatal(err)
				}

				if got.Version != expectedFOO {
					t.Fatalf("expected version %q, got %q", expectedFOO, got.Version)
				}
			})
		})
	}

	doTest(t, "foo/debug/resolve")
	doTest(t, "mariner2/debug/resolve")
	doTest(t, "debug/resolve")
}
