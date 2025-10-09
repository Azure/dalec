package frontend

import (
	"context"
	stderrors "errors"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/pkg/bkfs"
	"github.com/Azure/dalec/internal/testrunner"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

// RunTests runs the tests defined in the spec against the given target container.
func RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, ref gwclient.Reference, withTestDeps llb.StateOption, target string, platform *ocispecs.Platform) error {
	if skipVar := client.BuildOpts().Opts["build-arg:"+"DALEC_SKIP_TESTS"]; skipVar != "" {
		skip, err := strconv.ParseBool(skipVar)
		if err != nil {
			return errors.Wrapf(err, "could not parse build-arg %s", "DALEC_SKIP_TESTS")
		}
		if skip {
			Warn(ctx, client, llb.Scratch(), "Tests skipped due to build-arg DALEC_SKIP_TESTS="+skipVar)
			return nil
		}
	}

	tests := spec.Tests

	t, ok := spec.Targets[target]
	if ok {
		tests = append(tests, t.Tests...)
	}

	if len(tests) == 0 {
		return nil
	}

	// Force evaluation here so that any errors for the build itself can surface
	// more cleanly.
	// Otherwise an error for something wrong in the build (e.g. a failed compilation)
	// will look like an error in a test (or all tests).
	if err := ref.Evaluate(ctx); err != nil {
		return err
	}

	ctr, err := ref.ToState()
	if err != nil {
		return err
	}

	sOpt, err := SourceOptFromClient(ctx, client, platform)
	if err != nil {
		return err
	}

	ctrWithDeps := ctr.With(withTestDeps)

	frontendSt, err := GetCurrentFrontend(client)
	if err != nil {
		// This should never happen and indicates a bug in our implementation.
		// Nothing a user can do about it, so panic.
		panic(err)
	}

	var group errGroup

	const errorsOutputFile = "errors.txt"
	const outputPath = "/tmp/dalec/test/output"
	fullOutputPath := filepath.Join(outputPath, errorsOutputFile)

	for _, test := range tests {
		base := ctr
		for k, v := range test.Env {
			base = base.AddEnv(k, v)
		}

		var opts []llb.RunOption
		pg := llb.ProgressGroup(identity.NewID(), "Test: "+path.Join(target, test.Name), false)
		opts = append(opts, pg)

		for _, sm := range test.Mounts {
			opts = append(opts, sm.ToRunOption(sOpt, pg))
		}

		result := ctrWithDeps
		result = result.File(llb.Mkdir(outputPath, 0o755, llb.WithParents(true)), pg)

		for i, step := range test.Steps {
			opts := append(opts, testrunner.WithTestStep(frontendSt, &step, i, fullOutputPath))
			result = result.Run(opts...).Root()
		}

		if len(test.Files) > 0 {
			runOpts := append(opts, testrunner.WithFileChecks(frontendSt, test, fullOutputPath))
			result = result.Run(runOpts...).Root()
		}

		group.Go(func() (retErr error) {
			defer func() {
				if r := recover(); r != nil {
					trace := getPanicStack()
					retErr = errors.Errorf("panic running test %q: %v\n%s", test.Name, r, trace)
				}
			}()

			resultFS, err := bkfs.FromState(ctx, &result, client, dalec.Platform(platform))
			if err != nil {
				return errors.Wrap(err, "failed to run test "+test.Name)
			}

			p := strings.TrimPrefix(fullOutputPath, "/")
			dt, err := fs.ReadFile(resultFS, p)
			if err != nil {
				if !stderrors.Is(err, fs.ErrNotExist) {
					return errors.Wrapf(err, "failed to read file checks for test %q: %T", test.Name, err)
				}
				return nil
			}
			if len(dt) > 0 {
				return errors.Errorf("test %q failed:\n%s", test.Name, string(dt))
			}
			return nil
		})
	}

	return group.Wait()
}

type errGroup struct {
	group sync.WaitGroup
	mu    sync.Mutex
	errs  []error
}

func (g *errGroup) Go(f func() error) {
	g.group.Add(1)

	go func() {
		defer g.group.Done()
		g.mu.Lock()
		g.errs = append(g.errs, f())
		g.mu.Unlock()
	}()
}

func (g *errGroup) Wait() error {
	g.group.Wait()
	g.mu.Lock()
	defer g.mu.Unlock()

	err := stderrors.Join(g.errs...)
	g.errs = g.errs[:0]
	return err
}
