package rpm

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
)

// rpmbuildArgs returns the args of the exec op that runs rpmbuild.
func rpmbuildArgs(ctx context.Context, t *testing.T, st llb.State) string {
	t.Helper()

	for _, op := range test.LLBOpsFromState(ctx, t, st) {
		exec := op.Op.GetExec()
		if exec == nil {
			continue
		}
		args := strings.Join(exec.Meta.Args, " ")
		if strings.Contains(args, "rpmbuild") {
			return args
		}
	}

	t.Fatal("could not find rpmbuild exec op")
	return ""
}

func TestBuildRunsFullRPMBuild(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	args := rpmbuildArgs(ctx, t, Build(llb.Scratch(), llb.Image("worker"), "SPECS/foo/foo.spec", CacheInfo{}))

	// Pinned in full: changing this command invalidates the build cache for
	// every existing rpm build.
	const expect = `rpmbuild --define "_topdir /build/top" --define "_srcrpmdir /build/out/SRPMS" --define "_rpmdir /build/out/RPMS" --buildroot /build/tmp/work -ba SPECS/foo/foo.spec`
	assert.Assert(t, strings.HasSuffix(args, expect), args)
}

// TestBuildSourceRPMOnlyBuildsSource makes sure the source-only build never
// invokes the binary build. rpmbuild's `-bs` stops after the source package is
// created, so the spec's %build/%install sections are never executed.
func TestBuildSourceRPMOnlyBuildsSource(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	args := rpmbuildArgs(ctx, t, BuildSourceRPM(llb.Scratch(), llb.Image("worker"), "SPECS/foo/foo.spec"))

	assert.Assert(t, strings.Contains(args, " -bs SPECS/foo/foo.spec"), args)
	assert.Assert(t, !strings.Contains(args, " -ba "), args)
	// The source rpm must still land in the same location as a full build so
	// consumers (and the signer) see the same layout.
	assert.Assert(t, strings.Contains(args, `--define "_srcrpmdir /build/out/SRPMS"`), args)
	// No binary rpm dir is created in the output for a source-only build.
	assert.Assert(t, !strings.Contains(args, "_rpmdir"), args)
}
