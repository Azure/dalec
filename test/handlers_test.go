package test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/dalec"
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
	runTest := func(t *testing.T, f gwclient.BuildFunc) {
		t.Helper()
		ctx := startTestSpan(t)
		testEnv.RunTest(ctx, t, f)
	}

	t.Run("list targets", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			// Make sure phony is not in the list of targets since it shouldn't be registered in the base frontend.
			ls := listTargets(ctx, t, gwc, &dalec.Spec{
				Targets: map[string]dalec.Target{
					// Note: This is not setting the frontend image, so it should use the default frontend.
					"phony": {},
				},
			})

			checkTargetExists(t, ls, "debug/resolve")
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
			checkTargetExists(t, ls, "debug/resolve")
			checkTargetExists(t, ls, "phony/check")
			checkTargetExists(t, ls, "phony/debug/resolve")
			return gwclient.NewResult(), nil
		})
	})

	t.Run("execute target", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			spec := &dalec.Spec{
				Targets: map[string]dalec.Target{
					"phony": {
						Frontend: &dalec.Frontend{
							Image: phonyRef,
						},
					},
				}}

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
			dt := readFile(ctx, t, "hello", res)
			expect := []byte("phony hello")
			if !bytes.Equal(dt, expect) {
				t.Fatalf("expected %q, got %q", expect, string(dt))
			}

			// In this case we want to make sure that any targets that are registered by the frontend are namespaced by our target name prefix.
			// This is to ensure that the frontend is not overwriting any other targets.
			// Technically I suppose the target in the user-supplied spec could technically interfere with the base frontend, but that's not really a concern.
			// e.g. if a user-supplied target was called "debug" it could overwrite the "debug/resolve" target in the base frontend.

			sr = gwclient.SolveRequest{
				FrontendOpt: map[string]string{
					"target": "debug/resolve",
				},
			}
			specToSolveRequest(ctx, t, spec, &sr)

			res, err = gwc.Solve(ctx, sr)
			if err != nil {
				return nil, err
			}

			// The builtin debug/resolve target adds the resolved spec to /spec.yml, so check that its there.
			statFile(ctx, t, "spec.yml", res)

			sr = gwclient.SolveRequest{
				FrontendOpt: map[string]string{
					"target": "phony/debug/resolve",
				},
			}
			specToSolveRequest(ctx, t, spec, &sr)

			res, err = gwc.Solve(ctx, sr)
			if err != nil {
				return nil, err
			}

			checkFile(ctx, t, "resolve", res, []byte("phony resolve"))
			return gwclient.NewResult(), nil
		})
	})

	t.Run("target not found", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
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
			return gwclient.NewResult(), nil
		})
	})
}
