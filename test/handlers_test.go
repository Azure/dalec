package test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/fixtures"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
)

// TestHandlerTargetForwarding tests that the frontend forwards the build to the specified frontend image.
// In the case of this test we are forwarding to the same frontend image, but with a custom build target name.
// This should result in there being target with a prefix "foo", e.g. `"foo/debug/resolve"` instead of any
// of the built-in targets and forwards the build to the frontend image.
// Note: Without instrumenting the frontend with special things for testing, it is difficult to tell if
// the target was actually forwarded, but we can check that the target actually works.
func TestHandlerTargetForwarding(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(t)

	phonyRef := makeFrontendRef(ctx, t, "phony")
	opts := []testSolveOpt{withFrontend(phonyRef, fixtures.PhonyFrontend)}

	testEnv(ctx, t, func(ctx context.Context, t *testing.T, gwc gwclient.Client) {
		t.Run("forwarded target", func(t *testing.T) {
			ctx := startTestSpan(t)

			// Make sure phony is not in the list of targets since it shouldn't be registered in the base frontend.
			ls := listTargets(ctx, t, gwc, &dalec.Spec{
				Targets: map[string]dalec.Target{
					// Note: This is not setting the frontend image, so it should use the default frontend.
					"phony": {},
				},
			})
			if slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
				return strings.Contains(tgt.Name, "phony")
			}) {
				t.Fatal("found phony target")
			}

			// Now make sure the forwarded target works.
			spec := &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: phonyRef,
						},
					},
				}}

			// Make sure phony is in the list of targets since it should be registered in the forwarded frontend.
			ls = listTargets(ctx, t, gwc, spec)
			if !slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
				return tgt.Name == "phony/check"
			}) {
				t.Fatal("did not find phony/check target")
			}

			sr := gwclient.SolveRequest{
				FrontendOpt: map[string]string{
					"target": "phony/check",
				},
			}
			specToSolveRequest(ctx, t, spec, &sr)

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
							Image: phonyRef,
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

func listTargets(ctx context.Context, t *testing.T, gwc gwclient.Client, spec *dalec.Spec) targets.List {
	t.Helper()

	sr := gwclient.SolveRequest{
		FrontendOpt: map[string]string{"requestid": targets.RequestTargets},
	}

	specToSolveRequest(ctx, t, spec, &sr)

	res, err := gwc.Solve(ctx, sr)
	if err != nil {
		t.Fatalf("could not solve list targets: %v", err)
	}

	dt, ok := res.Metadata["result.json"]
	if !ok {
		t.Fatal("missing result.json from list targets")
	}

	var ls targets.List
	if err := json.Unmarshal(dt, &ls); err != nil {
		t.Fatalf("could not unmsarshal list targets result: %v", err)
	}
	return ls
}
