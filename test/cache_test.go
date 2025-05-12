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
	"github.com/Azure/dalec/targets"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
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
		{
			GoBuild: &dalec.GoBuildCache{
				Scope: randKey,
			},
		},
	}

	specWithCommand := func(cmds ...string) *dalec.Spec {
		cmds = append([]string{"set -ex;"}, cmds...)
		spec := newSimpleSpec()
		spec.Build.Caches = caches
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: strings.Join(cmds, "\n"),
		})
		return spec
	}

	getDir := func(t *testing.T, c dalec.CacheConfig) string {
		if c.Dir != nil {
			return c.Dir.Dest
		}
		if c.GoBuild != nil {
			return "${GOCACHE}"
		}
		t.Fatalf("invalid cache config or maybe the test needs to be updated for a new cache type?")
		return ""
	}

	distro, _, _ := strings.Cut(cfg.Package, "/")

	cmds := make([]string, 0, len(caches))

	// Makes sure the cache is populated with some data.
	populateCache := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		for i, c := range caches {
			cmds = append(cmds, fmt.Sprintf("echo %s %d > \"%s/hello\"", distro, i, getDir(t, c)))
		}

		spec := specWithCommand(cmds...)
		// Ignore the pkg buildkit cache for the pkg build to ensure our commands always run
		// Otherwise we can wind up in a case where this request is fully cached but there is nothing in the cache dirs.
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
		solveT(ctx, t, client, sr)
	}

	// Checks that a cache is pre-populated from anothwer build.
	checkCacheContents := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		for i, c := range caches {
			check := fmt.Sprintf("%s %d", distro, i)
			cmds = append(cmds, fmt.Sprintf("grep %q %s/hello\n", check, getDir(t, c)))
		}

		spec := specWithCommand(cmds...)
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)
	}

	// Used to validate that caches with namespaced dirs are not shared between distros and that
	// caches with no namespace are shared between distros.
	checkDistro := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		distro2 := "noble"
		if distro == distro2 {
			distro2 = "jammy"
		}

		t.Log("using distro", distro2)
		target := path.Join(distro2, "deb")

		// Note: cache3/hello3 should have the content written by the first test
		for i, c := range caches {
			dir := getDir(t, c)

			if c.Dir != nil && !c.Dir.NoAutoNamespace {
				cmds = append(cmds, fmt.Sprintf("[ -d %s ]; [ ! -f %s ]", dir, filepath.Join(dir, "hello")))
				continue
			}

			// We can't test gobuild here because it will have a different cache key due to using a different distro
			if c.GoBuild == nil {
				// Use the *original* distro name here since that is what wrote the file
				check := fmt.Sprintf("%s %d", distro, i)
				cmds = append(cmds, fmt.Sprintf("grep %q %s", check, filepath.Join(dir, "hello")))
			} else {
				// This should not exist because the gobuild cache is not shared between distros
				cmds = append(cmds, fmt.Sprintf("[ ! -f %q ]", filepath.Join(dir, "hello")))
			}
		}

		spec := specWithCommand(cmds...)
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(target))
		solveT(ctx, t, client, sr)
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		// Make sure the cache is populated
		populateCache(ctx, t, client)

		// Now make sure the cache is persistent by running another build and checking the cache contents
		cmds = cmds[:0]
		checkCacheContents(ctx, t, client)

		// Now make sure the cache is not shared with another distro
		// This makes sure that that the 1st and 2nd cache are not shared with another distro, but the 3rd one is.
		cmds = cmds[:0]
		checkDistro(ctx, t, client)
	})
}

func testAutoGobuildCache(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	specWithCommand := func(cmd string) *dalec.Spec {
		spec := newSimpleSpec()
		spec.Dependencies = &dalec.PackageDependencies{}
		spec.Dependencies.Build = map[string]dalec.PackageConstraints{
			cfg.GetPackage("golang"): {},
		}
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: cmd,
		})
		return spec
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -d \"$GOCACHE\" ]; echo hello > ${GOCACHE}/hello\n")

		spec := specWithCommand(buf.String())
		// Set ignore cache to make sure we always run the command so the cache is guaranteed to be populated
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
		solveT(ctx, t, client, sr)

		buf.Reset()
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -d \"$GOCACHE\" ]; grep hello ${GOCACHE}/hello\n")
		spec = specWithCommand(buf.String())

		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Now disable the auto gobuild cache
		spec = specWithCommand("[ -z \"${GOCACHE}\" ]")
		spec.Build.Caches = []dalec.CacheConfig{
			{GoBuild: &dalec.GoBuildCache{Disabled: true}},
		}
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Also make sure there is no autocache when there is no golang dependency
		spec = specWithCommand("[ -z \"${GOCACHE}\" ]")
		spec.Dependencies = nil
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)
	})
}
