package test

import (
	"context"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
)

// testPatchGitattributesBinaryFile is a regression test for the deb source
// package flow dropping patch changes to files a source marks as non-diffable
// (`-diff`) in .gitattributes. createPatches captures patches via `git diff`;
// without `--text`, git emits "Binary files differ" for such files and the
// change is lost, so the built package keeps the unpatched content. This mirrors
// generated `*.pb.go` files (which upstreams mark `-diff`) being left stale
// during a gomod vendor rewrite.
func testPatchGitattributesBinaryFile(ctx context.Context, t *testing.T, targetCfg targetConfig) {
	const patch = `--- a/marked.txt
+++ b/marked.txt
@@ -1 +1 @@
-OLD
+NEW
`
	strip := 1
	spec := &dalec.Spec{
		Name:        "test-dalec-gitattr-patch",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Description: "Regression: patches to files a source marks -diff must still apply",
		Sources: map[string]dalec.Source{
			"src": {
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							".gitattributes": {Contents: "marked.txt -diff\n"},
							"marked.txt":     {Contents: "OLD\n"},
						},
					},
				},
			},
			"the-patch": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{Contents: patch},
				},
			},
		},
		Patches: map[string][]dalec.PatchSpec{
			"src": {{Source: "the-patch", Strip: &strip}},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				// Fails the build if the patch to the -diff-marked file was dropped.
				{Command: "grep -qx NEW ./src/marked.txt"},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Package))
		solveT(ctx, t, gwc, sr)
	})
}
