package test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/fixtures"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

// TestHandlerTargetForwarding tests that the frontend forwards the build to the specified frontend image.
// In the case of this test we are forwarding to the same frontend image, but with a custom build target name.
// This should result in there being target with a prefix "foo", e.g. `"foo/debug/resolve"` instead of any
// of the built-in targets and forwards the build to the frontend image.
// Note: Without instrumenting the frontend with special things for testing, it is difficult to tell if
// the target was actually forwarded, but we can check that the target actually works.
func TestHandlerTargetForwarding(t *testing.T) {
	t.Parallel()

	opts := []testSolveOpt{withFrontend("phony", fixtures.PhonyFrontend)}

	testEnv(t, func(ctx context.Context, t *testing.T, gwc gwclient.Client) {
		t.Run("forwarded target", func(t *testing.T) {
			// First, make sure this target isn't in the base frontend.
			sr := gwclient.SolveRequest{
				FrontendOpt: map[string]string{
					"target": "phony/check",
				},
			}
			specToSolveRequest(ctx, t, &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {},
				}}, &sr)

			_, err := gwc.Solve(ctx, sr)
			expectUnknown := "unknown target"
			if err == nil || !strings.Contains(err.Error(), expectUnknown) {
				t.Fatalf("expected error %q, got %v", expectUnknown, err)
			}

			// Now make sure the forwarded target works.
			sr = gwclient.SolveRequest{
				FrontendOpt: map[string]string{
					"target": "phony/check",
				},
			}
			specToSolveRequest(ctx, t, &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: "phony",
						},
					},
				}}, &sr)

			res, err := gwc.Solve(ctx, sr)
			if err != nil {
				t.Fatal(err)
			}
			dt := readFileResult(ctx, t, "hello", res)
			expect := []byte("phony hello")
			if !bytes.Equal(dt, expect) {
				t.Fatalf("expected %q, got %q", expect, string(dt))
			}
		})

		t.Run("target not found", func(t *testing.T) {
			sr := gwclient.SolveRequest{
				FrontendOpt: map[string]string{
					"target": "phony/does-not-exist",
				},
			}
			specToSolveRequest(ctx, t, &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: "phony",
						},
					},
				},
			}, &sr)
			_, err := gwc.Solve(ctx, sr)
			expect := "unknown target"
			if err == nil || !strings.Contains(err.Error(), expect) {
				t.Fatalf("expected error %q, got %v", expect, err)
			}
		})

	}, opts...)

}

func readFileResult(ctx context.Context, t *testing.T, name string, res *gwclient.Result) []byte {
	t.Helper()

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: name,
	})
	if err != nil {
		t.Fatal(err)
	}

	return dt
}
