package test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"gotest.tools/v3/assert"
)

func testArtifactBuildCacheDir(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	// Add a random key to all the to make sure they are unique
	// for test runs and parallel tests don't interfere with each other.
	// We also use this key in each of the dest paths to force cache invalidation.
	// This is important because we need each case to actually run uncached (at least from the actual build part)
	// otherwise the test will almost certainly fail if the part that writes data is cached but the part that reads it is not.
	buf := make([]byte, 16)
	n, _ := rand.Read(buf)
	randKey := hex.EncodeToString(buf[:n])

	caches := []dalec.CacheConfig{
		{
			Dir: &dalec.CacheDir{
				Dest: filepath.Join("/tmp/cache", randKey+"1"),
			},
		},
		{
			Dir: &dalec.CacheDir{
				Key:  randKey,
				Dest: filepath.Join("/tmp/cache", randKey+"2"),
			},
		},
		{
			Dir: &dalec.CacheDir{
				Key:             randKey,
				Dest:            filepath.Join("/tmp/cache", randKey+"3"),
				NoAutoNamespace: true,
			},
		},
	}

	specWithCommand := func(cmd string) *dalec.Spec {
		spec := newSimpleSpec()
		spec.Build.Caches = caches
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: cmd,
		})
		return spec
	}

	distro, _, _ := strings.Cut(cfg.Package, "/")

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		// Make sure the cache is populated
		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex;\n")

		for i, c := range caches {
			fmt.Fprintf(buf, "echo %s %d > %s\n", distro, i, filepath.Join(c.Dir.Dest, "hello"))
		}

		spec := specWithCommand(buf.String())
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Now make sure the cache is persistent
		buf.Reset()
		buf.WriteString("set -ex;\n")

		for i, c := range caches {
			check := fmt.Sprintf("%s %d", distro, i)
			fmt.Fprintf(buf, "grep %q %s\n", check, filepath.Join(c.Dir.Dest, "hello"))
		}

		spec = specWithCommand(buf.String())
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// This makes sure that that the 1st and 2nd cache are not shared with another distro, but the 3rd one is.
		distro2 := "noble"
		if distro == distro2 {
			distro2 = "jammy"
		}
		t.Log("using distro2", distro2)
		target := path.Join(distro2, "deb")

		// Note: cache3/hello3 should have the content written by the first test
		buf.Reset()
		buf.WriteString("set -ex;\n")

		fmt.Fprintln(buf, "cat \"$0\"")

		for i, c := range caches {
			if !c.Dir.NoAutoNamespace {
				fmt.Fprintf(buf, "[ -d %s ]; [ ! -f %s ]\n", c.Dir.Dest, filepath.Join(c.Dir.Dest, "hello"))
				continue
			}

			check := fmt.Sprintf("%s %d", distro, i)
			fmt.Fprintf(buf, "grep %q %s\n", check, filepath.Join(c.Dir.Dest, "hello"))
		}

		spec = specWithCommand(buf.String())
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(target))
		res := solveT(ctx, t, client, sr)
		_, err := res.SingleRef()
		assert.NilError(t, err)
	})
}
