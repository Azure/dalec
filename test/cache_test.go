package test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/sessionutil/socketprovider"
	"github.com/Azure/dalec/targets"
	"github.com/Azure/dalec/test/testenv"
	"github.com/buchgr/bazel-remote/v2/cache/disk"
	bazelremote "github.com/buchgr/bazel-remote/v2/server"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"google.golang.org/grpc"
	"gotest.tools/v3/assert"
)

func testArtifactBuildCacheDir(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	// Add a random key to all the to make sure they are unique
	// for test runs and parallel tests don't interfere with each other.
	// We also use this key in each of the dest paths to force cache invalidation.
	// This is important because we need each case to actually run uncached (at least from the actual build part)
	// otherwise the test will almost certainly fail if the part that writes data is cached but the part that reads it is not.
	randKey := getRand()

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
		{
			Bazel: &dalec.BazelCache{
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
		if c.Bazel != nil {
			// There is no good way to determine the bazel cache dir
			// So just hardcode this for now
			return "/tmp/dalec/bazel-local-cache"
		}
		t.Fatalf("invalid cache config or maybe the test needs to be updated for a new cache type?")
		return ""
	}

	distro, _, _ := strings.Cut(cfg.Package, "/")

	cmds := make([]string, 0, len(caches))

	// Makes sure the cache is populated with some data.
	populateCache := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		for i, c := range caches {
			dir := getDir(t, c)
			cmds = append(cmds, fmt.Sprintf("echo %s %d > \"%s/hello\"", distro, i, dir))

			if c.Bazel != nil {
				cmds = append(cmds, fmt.Sprintf("grep %q /etc/bazel.bazelrc && exit; cat /etc/bazel.bazelrc; exit 42", dir))
			}
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

			switch {
			case c.Dir != nil:
				// Use the *original* distro name here since that is what wrote the file
				check := fmt.Sprintf("%s %d", distro, i)
				cmds = append(cmds, fmt.Sprintf("grep %q %s", check, filepath.Join(dir, "hello")))
			case c.GoBuild != nil || c.Bazel != nil:
				// This should not exist because the cache is not shared between distros
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

func getRand() string {
	buf := make([]byte, 16)
	n, _ := rand.Read(buf)
	return hex.EncodeToString(buf[:n])
}

func testBazelCache(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	bzlPkg := cfg.GetPackage("bazel")
	if bzlPkg == noPackageAvailable {
		t.Skip("bazel not available in this distro")
	}

	// Add a random key to all the to make sure they are unique
	// for test runs and parallel tests don't interfere with each other.
	// We also use this key in each of the dest paths to force cache invalidation.
	// This is important because we need each case to actually run uncached (at least from the actual build part)
	// otherwise the test will almost certainly fail if the part that writes data is cached but the part that reads it is not.
	randKey := getRand()

	newSpec := func(cmds ...string) *dalec.Spec {
		spec := newSimpleSpec()
		spec.Build.Caches = []dalec.CacheConfig{
			{
				Bazel: &dalec.BazelCache{
					Scope: randKey,
				},
			},
		}

		spec.Dependencies = &dalec.PackageDependencies{}
		spec.Dependencies.Build = map[string]dalec.PackageConstraints{
			"curl": {},
			bzlPkg: {},
		}
		spec.Sources["src"] = dalec.Source{
			Inline: &dalec.SourceInline{
				Dir: &dalec.SourceInlineDir{
					Files: map[string]*dalec.SourceInlineFile{
						"WORKSPACE": {
							Contents: "workspace(name = \"hello\")\n",
						},
						"BUILD": {
							Contents: `
genrule(
    name = "hello",
    outs = ["hello.txt"],
    cmd = "echo 'hello from bazel' > $@",
)
`,
						},
						"hello.txt": {
							Contents: "hello\n",
						},
					},
				},
			},
		}

		cmds = append([]string{"set -ex;"}, cmds...)
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: strings.Join(cmds, "\n"),
		})

		return spec
	}

	dirCmd := "dir=$(grep disk_cache /etc/bazel.bazelrc | awk -F'=' '{ print $2 }')"

	// Setup the bazel "remote" cache server.
	var pipeL socketprovider.PipeListener
	defer pipeL.Close()

	srv := grpc.NewServer()
	cacheDir := t.TempDir()
	logger := &bazelRemoteLogger{t}

	log.SetOutput(io.Discard) // Disable bazel-remote's default logging to avoid cluttering the test output
	diskCache, err := disk.New(cacheDir, 10*1024*1024, disk.WithAccessLogger(log.New(io.Discard, "", 0)))
	log.SetOutput(os.Stderr) // Restore logging to stderr for bazel-remote
	assert.NilError(t, err)

	origPaths := make(map[string]struct{}, 20)
	err = filepath.Walk(cacheDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		origPaths[p] = struct{}{}
		return nil
	})
	assert.NilError(t, err)

	go bazelremote.ServeGRPC(&pipeL, srv, false, false, true, diskCache, logger, logger)

	// Write to the bazel cache using bazel itself
	spec := newSpec(`[ ! -d "${dir}/ac" ]`, `[ ! -d "${dir}/cas" ]`, "[ -S /tmp/dalec/bazel-remote.sock ]", "cd src; bazel build --announce_rc //:hello")
	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
		solveT(ctx, t, client, sr)
	}, testenv.WithSocketProxies(socketprovider.ProxyConfig{
		ID:     dalec.BazelDefaultSocketID,
		Dialer: pipeL.Dialer,
	}))

	// Now validate that bazel wrote to the cache
	spec = newSpec(dirCmd, `[ -d "${dir}/ac" ]`, `[ -d "${dir}/cas" ]`)
	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)
	})

	// Make sure bazel wrote to our "remote" cache.
	newPaths := make(map[string]struct{}, 20)
	err = filepath.Walk(cacheDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if _, ok := origPaths[p]; ok {
			return nil
		}
		newPaths[p] = struct{}{}
		return nil
	})
	assert.NilError(t, err)
	assert.Assert(t, len(newPaths) > 0, "expected bazel to write to the cache")
}

type bazelRemoteLogger struct {
	t *testing.T
}

func (l *bazelRemoteLogger) Printf(format string, args ...interface{}) {
	l.t.Logf(format, args...)
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
