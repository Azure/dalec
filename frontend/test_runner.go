package frontend

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strconv"

	"github.com/Azure/dalec"
	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/pkg/errors"
)

// Run tests runs the tests defined in the spec against the given target container.
func RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, ref gwclient.Reference, withTestDeps llb.StateOption, target string, opts ...llb.ConstraintsOpt) error {
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

	ctr, err := ref.ToState()
	if err != nil {
		return err
	}

	sOpt, err := SourceOptFromClient(ctx, client)
	if err != nil {
		return err
	}

	ctrWithDeps := ctr.With(withTestDeps)
	states := make([]llb.State, 0, len(tests))

	for _, test := range tests {
		base := ctr
		for k, v := range test.Env {
			base = base.AddEnv(k, v)
		}

		pg := dalec.ProgressGroup("Test: " + path.Join(target, test.Name))
		opts := append(opts, pg)

		execOpts := []llb.RunOption{dalec.WithConstraints(opts...)}
		execOpts = append(execOpts, dalec.CacheDirsToRunOpt(test.CacheDirs, "", ""))

		for _, sm := range test.Mounts {
			st, err := sm.Spec.AsMount(sm.Dest, sOpt, opts...)
			if err != nil {
				return err
			}
			execOpts = append(execOpts, llb.AddMount(sm.Dest, st, llb.SourcePath(sm.Dest)))
		}

		opts = append(opts, pg)
		if len(test.Steps) == 0 {
			st := base.Async(runTest(test, nil, client, opts...))
			states = append(states, st)
			continue
		}

		worker := ctrWithDeps

		var needsStdioMount bool
		ios := map[int]llb.State{}
		for i, step := range test.Steps {
			var stepOpts []llb.RunOption
			id := identity.NewID()
			ioSt := llb.Scratch()
			if step.Stdin != "" {
				needsStdioMount = true
				stepOpts = append(stepOpts, llb.AddEnv("STDIN_FILE", filepath.Join("/tmp", id, "stdin")))
				ioSt = ioSt.File(llb.Mkfile("stdin", 0444, []byte(step.Stdin)), opts...)
			}
			if !step.Stdout.IsEmpty() {
				needsStdioMount = true
				stepOpts = append(stepOpts, llb.AddEnv("STDOUT_FILE", path.Join("/tmp", id, "stdout")))
				ioSt = ioSt.File(llb.Mkfile("stdout", 0664, nil), opts...)
			}

			if !step.Stderr.IsEmpty() {
				needsStdioMount = true
				stepOpts = append(stepOpts, llb.AddEnv("STDERR_FILE", path.Join("/tmp", id, "stderr")))
				ioSt = ioSt.File(llb.Mkfile("stderr", 0664, nil), opts...)
			}

			cmd, err := shlex.Split(step.Command)
			if err != nil {
				return err
			}
			if needsStdioMount {
				fSt, err := client.(frontendClient).CurrentFrontend()
				if err != nil {
					return err
				}
				p := filepath.Join("/tmp", id+"-2", "dalec-redirectio")
				stepOpts = append(stepOpts, llb.AddMount(p, *fSt, llb.SourcePath("/dalec-redirectio")))
				cmd = append([]string{p}, cmd...)
			}

			stepOpts = append(stepOpts, llb.Args(cmd))
			stepOpts = append(stepOpts, llb.With(func(s llb.State) llb.State {
				for k, v := range step.Env {
					s = s.AddEnv(k, v)
				}

				return s
			}))

			est := worker.Run(stepOpts...)
			if needsStdioMount {
				ioSt = est.AddMount(filepath.Join("/tmp", id), ioSt)
				ios[i] = ioSt
			}
			worker = est.Root()
		}

		st := worker.Async(runTest(test, ios, client, opts...))
		states = append(states, st)
	}

	refs := make([]gwclient.Reference, 0, len(states))
	for _, st := range states {
		def, err := st.Marshal(ctx, opts...)
		if err != nil {
			return err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return err
		}
		refs = append(refs, ref)
	}

	return evalRefs(ctx, refs)
}

type frontendClient interface {
	CurrentFrontend() (*llb.State, error)
}

func checkFile(ctx context.Context, ref gwclient.Reference, p string, check dalec.FileCheckOutput) error {
	stat, err := ref.StatFile(ctx, gwclient.StatRequest{
		Path: p,
	})
	if err != nil {
		if check.NotExist {
			// TODO: buildkit just gives a generic error here (with grpc code `Unknown`)
			// There's not really a good way to determine if the error is because the file is missing or something else.
			return nil
		}

		return errors.Wrap(err, "stat failed")
	}

	if stat != nil && check.NotExist {
		return errors.Wrap(err, "file exists but should not")
	}

	var dt []byte
	if !check.CheckOutput.IsEmpty() {
		dt, err = ref.ReadFile(ctx, gwclient.ReadRequest{
			Filename: p,
		})
		if err != nil {
			return errors.Wrap(err, "read failed")
		}
	}

	if err := check.Check(string(dt), fs.FileMode(stat.Mode), stat.IsDir(), p); err != nil {
		return errors.WithStack(err)
	}
	return nil
}

func checkStdio(client gwclient.Client, ionum int, t *dalec.TestSpec, opts ...llb.ConstraintsOpt) asyncStateFunc {
	return func(ctx context.Context, st llb.State, constraints *llb.Constraints) (llb.State, error) {
		for _, o := range opts {
			o.SetConstraintsOption(constraints)
		}

		def, err := st.Marshal(ctx, withConstraints(constraints))
		if err != nil {
			return st, errors.Wrap(err, "failed to marshal stdio state")
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
			Evaluate:   true,
		})
		if err != nil {
			return st, errors.Wrap(err, "failed to solve stdio state")
		}

		ref, err := res.SingleRef()
		if err != nil {
			return st, errors.Wrapf(err, "failed to get stdio ref for %d", ionum)
		}

		checkFile := func(c dalec.CheckOutput, name string) error {
			if c.IsEmpty() {
				return nil
			}
			dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
				Filename: path.Join(name),
			})
			if err != nil {
				return errors.Wrapf(err, "%s: read failed", name)
			}
			if err := c.Check(string(dt), name); err != nil {
				return errors.Wrap(err, name)
			}
			return nil
		}

		var outErr error
		step := t.Steps[ionum]
		if err := checkFile(step.Stdout, "stdout"); err != nil {
			outErr = stderrors.Join(outErr, err)
		}
		if err := checkFile(step.Stderr, "stderr"); err != nil {
			outErr = stderrors.Join(err)
		}
		return st, outErr
	}
}

func evalRefs(ctx context.Context, refs []gwclient.Reference) error {
	ch := make(chan error, len(refs))
	var outErr error

	for _, ref := range refs {
		go func() {
			ch <- ref.Evaluate(ctx)
		}()
	}

	for i := 0; i < len(refs); i++ {
		select {
		case <-ctx.Done():
		case err := <-ch:
			if err != nil {
				outErr = stderrors.Join(outErr, err)
			}
		}
	}
	return outErr
}

func runTest(t *dalec.TestSpec, ios map[int]llb.State, client gwclient.Client, opts ...llb.ConstraintsOpt) asyncStateFunc {
	return func(ctx context.Context, st llb.State, constraints *llb.Constraints) (llb.State, error) {
		for _, o := range opts {
			o.SetConstraintsOption(constraints)
		}

		def, err := st.Marshal(ctx, withConstraints(constraints))
		if err != nil {
			return st, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
			Evaluate:   true,
		})
		if err != nil {
			return st, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return st, err
		}

		var outErr error
		for p, check := range t.Files {
			if err := checkFile(ctx, ref, p, check); err != nil {
				outErr = stderrors.Join(outErr, fmt.Errorf("%s: %w", p, err))
			}
		}

		refs := make([]gwclient.Reference, 0, len(ios))

		for i, st := range ios {
			st := st.Async(checkStdio(client, i, t, opts...))
			def, err := st.Marshal(ctx, opts...)
			if err != nil {
				return st, err
			}

			res, err := client.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return st, err
			}

			ref, err := res.SingleRef()
			if err != nil {
				return st, err
			}
			refs = append(refs, ref)
		}

		return st, stderrors.Join(outErr, evalRefs(ctx, refs))
	}
}
