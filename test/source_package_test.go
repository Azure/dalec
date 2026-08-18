package test

import (
	"context"
	"strings"
	"testing"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
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

// testSourceOutputAppliesGomodEdits verifies that spec preprocessing (gomod
// replace directives) is applied when producing the deb source package output.
// The handler previously skipped Preprocess, silently emitting a source package
// missing the generated gomod patch.
func testSourceOutputAppliesGomodEdits(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	if !strings.HasSuffix(targetCfg.Package, "/deb") {
		t.Skip("source-package preprocessing check only implemented for deb /dsc")
	}
	sourceTarget := strings.TrimSuffix(targetCfg.Package, "/deb") + "/dsc"

	spec := &dalec.Spec{
		Name:        "test-dalec-dsc-preprocess",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Description: "gomod edits must be preprocessed into the source package",
		Sources: map[string]dalec.Source{
			"src": {
				Generate: []*dalec.SourceGenerator{
					{
						Gomod: &dalec.GeneratorGomod{
							Edits: &dalec.GomodEdits{
								Replace: []dalec.GomodReplace{
									{Original: "github.com/cpuguy83/tar2go@v0.3.1", Update: "github.com/cpuguy83/tar2go@v0.3.0"},
								},
							},
						},
					},
				},
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							"main.go": {Contents: gomodFixtureMain},
							// go 1.18 so `go mod tidy` (run by Preprocess) works on
							// distros shipping older Go toolchains (e.g. Jammy's 1.18).
							"go.mod": {Contents: "module testgomodsource\n\ngo 1.18\n\nrequire github.com/cpuguy83/tar2go v0.3.1\n"},
							"go.sum": {Contents: gomodFixtureSum},
						},
					},
				},
			},
		},
		Dependencies: &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				targetCfg.GetPackage("golang"): {},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		res := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(sourceTarget)))

		// Pull dalec-changes.patch out of the source package and confirm the
		// gomod replace directive made it in (i.e. Preprocess ran).
		st := llb.Image("alpine:latest").
			Run(dalec.ShArgs("apk add --no-cache tar xz")).Root().
			Run(
				dalec.ShArgs(`set -e; mkdir -p /out /x; for f in /src/*.debian.tar.xz; do tar -xJf "$f" -C /x; done; cp /x/debian/patches/dalec-changes.patch /out/patch`),
				llb.AddMount("/src", resultToState(t, res), llb.Readonly),
			).AddMount("/out", llb.Scratch())

		def, err := st.Marshal(ctx)
		assert.NilError(t, err)
		out, err := gwc.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB(), Evaluate: true})
		assert.NilError(t, err)

		patch := string(readFile(ctx, t, "patch", out))
		assert.Check(t, strings.Contains(patch, "replace github.com/cpuguy83/tar2go"),
			"source package patch must contain the gomod replace directive (Preprocess must run for source packages), got:\n%s", patch)
	})
}

// testSourceRPMTarget verifies the `<distro>/srpm` target produces the same
// source rpm a full `<distro>/rpm` build produces, without running the spec's
// build steps (i.e. rpmbuild's %build/binary rpm stages never run).
func testSourceRPMTarget(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	if !strings.HasSuffix(targetCfg.Package, "/rpm") {
		t.Skipf("srpm target is not available for package target %q", targetCfg.Package)
	}
	srpmTarget := strings.TrimSuffix(targetCfg.Package, "/rpm") + "/srpm"

	spec := &dalec.Spec{
		Name:        "test-dalec-srpm-target",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "Testing the source rpm only target",
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
				// The srpm target must not execute build steps. If %build runs
				// this fails the build and therefore the test.
				{Command: "exit 42"},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(srpmTarget))
		res := solveT(ctx, t, gwc, sr)

		ref, err := res.SingleRef()
		assert.NilError(t, err)

		// The source rpm must land in the same place a full rpm build puts it.
		srpmPath := expectedSRPMPath(t, targetCfg, spec)
		_, err = ref.StatFile(ctx, gwclient.StatRequest{Path: srpmPath})
		assert.NilError(t, err, "expected source rpm at %q", srpmPath)

		// ... and nothing else: with `-bs` rpmbuild never populates the binary
		// rpm output dir, so `SRPMS` must be the only thing in the output.
		ents, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{Path: "/"})
		assert.NilError(t, err)

		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Path)
		}
		assert.Check(t, cmp.DeepEqual(names, []string{"SRPMS"}), "srpm target output should only contain SRPMS, got %v", names)
	})
}

// expectedSRPMPath returns the path of the source rpm the distro's rpm target
// produces for the given spec.
func expectedSRPMPath(t *testing.T, targetCfg targetConfig, spec *dalec.Spec) string {
	t.Helper()

	if targetCfg.ListExpectedSignFiles == nil {
		t.Fatal("target config is missing ListExpectedSignFiles")
	}

	for _, f := range targetCfg.ListExpectedSignFiles(spec, platforms.DefaultSpec()) {
		if strings.HasSuffix(f, ".src.rpm") {
			return f
		}
	}

	t.Fatal("no source rpm in the list of expected package files")
	return ""
}
