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
	"github.com/moby/buildkit/identity"
)

// TestHandlerTargetForwarding tests that the frontend forwards the build to the specified frontend image.
// In the case of this test we are forwarding to the same frontend image, but with a custom build target name.
// This should result in there being target with a prefix "foo", e.g. `"foo/debug/resolve"` instead of any
// of the built-in targets and forwards the build to the frontend image.
// Note: Without instrumenting the frontend with special things for testing, it is difficult to tell if
// the target was actually forwarded, but we can check that the target actually works.
func TestHandlerTargetForwarding(t *testing.T) {
	// Only generate a ref once
	// It shouldn't be *too* expensive, but it's not necessary to do it for every test.
	phonyRef := identity.NewID()

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
			if slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
				return strings.Contains(tgt.Name, "phony")
			}) {
				t.Fatal("found phony target")
			}

			// Add the custom frontend to the build env so that dalec can resolve the target.
			gwc = wrapWithInput(gwc, phonyRef, fixtures.PhonyFrontend)

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

			if !slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
				return tgt.Name == "phony/debug/resolve"
			}) {
				t.Fatal("did not find phony/check target")
			}

			if !slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
				return tgt.Name == "debug/resolve"
			}) {
				t.Fatal("did not find phony/check target")
			}
			return gwclient.NewResult(), nil
		})
	})

	t.Run("execute target", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			phonyRef := identity.NewID()
			gwc = wrapWithInput(gwc, phonyRef, fixtures.PhonyFrontend)
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
			dt := readFileResult(ctx, t, "hello", res)
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
			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			// The builtin debug/resolve target adds the resolved spec to /spec.yml, so check that its there.
			_, err = ref.StatFile(ctx, gwclient.StatRequest{
				Path: "spec.yml",
			})
			if err != nil {
				t.Fatalf("expected spec.yml to exist in debug/resolve target, got error: %v", err)
			}

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
			ref, err = res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			// The phony/debug/resolve target adds a dummy file to /resolve
			dt, err = ref.ReadFile(ctx, gwclient.ReadRequest{
				Filename: "resolve",
			})
			if err != nil {
				t.Fatal(err)
			}
			expect = []byte("phony resolve")
			if !bytes.Equal(dt, expect) {
				t.Fatalf("expected %q, got %q", string(expect), string(dt))
			}

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

			// Add the custom frontend to the build env so that dalec can resolve the target.
			gwc = wrapWithInput(gwc, phonyRef, fixtures.PhonyFrontend)

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
