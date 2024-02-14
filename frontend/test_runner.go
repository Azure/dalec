package frontend

import (
	"context"
	stderrors "errors"
	"io/fs"
	"path"
	"path/filepath"
	"sync"

	"github.com/Azure/dalec"
	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/pkg/errors"
)

// Run tests runs the tests defined in the spec against the given target container.
func RunTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, ref gwclient.Reference, target string) error {
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

	type testPair struct {
		st     llb.State
		t      *dalec.TestSpec
		stdios map[int]llb.State
	}

	runs := make([]testPair, 0, len(tests))
	for _, test := range tests {
		base := ctr
		for k, v := range test.Env {
			base = base.AddEnv(k, v)
		}

		var opts []llb.RunOption
		opts = append(opts, dalec.CacheDirsToRunOpt(test.CacheDirs, "", ""))

		pg := llb.ProgressGroup(identity.NewID(), "Test: "+path.Join(target, test.Name), false)

		for _, sm := range test.Mounts {
			st, err := dalec.Source2LLBGetter(spec, sm.Spec, sm.Dest, sOpt, pg)
			if err != nil {
				return err
			}
			opts = append(opts, llb.AddMount(sm.Dest, st, llb.SourcePath(sm.Dest)))
		}

		opts = append(opts, pg)
		if len(test.Steps) > 0 {
			var worker llb.State
			var needsStdioMount bool
			ios := map[int]llb.State{}
			for i, step := range test.Steps {
				var stepOpts []llb.RunOption
				id := identity.NewID()
				ioSt := llb.Scratch()
				if step.Stdin != "" {
					needsStdioMount = true
					stepOpts = append(stepOpts, llb.AddEnv("STDIN_FILE", filepath.Join("/tmp", id, "stdin")))
					ioSt = ioSt.File(llb.Mkfile("stdin", 0444, []byte(step.Stdin)))
				}
				if !step.Stdout.IsEmpty() {
					needsStdioMount = true
					stepOpts = append(stepOpts, llb.AddEnv("STDOUT_FILE", path.Join("/tmp", id, "stdout")))
					ioSt = ioSt.File(llb.Mkfile("stdout", 0664, nil))
				}

				if !step.Stderr.IsEmpty() {
					needsStdioMount = true
					stepOpts = append(stepOpts, llb.AddEnv("STDERR_FILE", path.Join("/tmp", id, "stderr")))
					ioSt = ioSt.File(llb.Mkfile("stderr", 0664, nil))
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
				stepOpts = append(opts, stepOpts...)

				var est llb.ExecState
				if i == 0 {
					est = base.Run(stepOpts...)
				} else {
					est = worker.Run(stepOpts...)
				}
				if needsStdioMount {
					ioSt = est.AddMount(filepath.Join("/tmp", id), ioSt)
					ios[i] = ioSt
				}
				worker = est.Root()
			}

			runs = append(runs, testPair{st: worker, t: test, stdios: ios})
		} else {
			runs = append(runs, testPair{st: base, t: test})
		}
	}

	var errs errorList
	var wg sync.WaitGroup
	for _, pair := range runs {
		pair := pair
		wg.Add(1)
		go func() {
			if err := runTest(ctx, pair.t, pair.st, pair.stdios, client); err != nil {
				errs.Append(errors.Wrap(err, "FAILED: "+path.Join(target, pair.t.Name)))
			}
			wg.Done()
		}()
	}

	wg.Wait()

	return errs.Join()
}

type frontendClient interface {
	CurrentFrontend() (*llb.State, error)
}

func runTest(ctx context.Context, t *dalec.TestSpec, st llb.State, ios map[int]llb.State, client gwclient.Client) error {
	def, err := st.Marshal(ctx)
	if err != nil {
		return err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
		Evaluate:   true,
	})
	if err != nil {
		return err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return err
	}

	for p, check := range t.Files {
		stat, err := ref.StatFile(ctx, gwclient.StatRequest{
			Path: p,
		})
		if err != nil {
			if check.NotExist {
				// TODO: buildkit just gives a generic error here (with grpc code `Unknown`)
				// There's not really a good way to determine if the error is because the file is missing or something else.
				continue
			}
			return errors.Wrapf(err, "stat failed: %s", p)
		}

		if stat != nil && check.NotExist {
			return errors.Errorf("file %s exists but should not", p)
		}

		var dt []byte
		if !check.CheckOutput.IsEmpty() {
			dt, err = ref.ReadFile(ctx, gwclient.ReadRequest{
				Filename: p,
			})
			if err != nil {
				return errors.Wrapf(err, "read failed: %s", p)
			}
		}
		if err := check.Check(string(dt), fs.FileMode(stat.Mode), stat.IsDir(), p); err != nil {
			return errors.WithStack(err)
		}
	}

	for i, st := range ios {
		def, err := st.Marshal(ctx)
		if err != nil {
			return err
		}
		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
			Evaluate:   true,
		})
		if err != nil {
			return err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return err
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

		step := t.Steps[i]
		if err := checkFile(step.Stdout, "stdout"); err != nil {
			return err
		}
		if err := checkFile(step.Stderr, "stderr"); err != nil {
			return err
		}
	}

	return nil
}

type errorList struct {
	mu sync.Mutex
	ls []error
}

func (e *errorList) Append(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.ls = append(e.ls, err)
	e.mu.Unlock()
}

func (e *errorList) Join() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.ls) == 0 {
		return nil
	}

	return stderrors.Join(e.ls...)
}
