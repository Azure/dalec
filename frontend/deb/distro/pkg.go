package distro

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/Azure/dalec/frontend/pkg/bkfs"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (d *Config) BuildDeb(ctx context.Context, worker llb.State, sOpt dalec.SourceOpts, client gwclient.Client, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Build deb package"))

	versionID := d.VersionID
	if versionID == "" {
		var err error
		versionID, err = deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return worker, err
		}
	}

	worker = worker.With(d.InstallBuildDeps(sOpt, spec, targetKey))
	srcPkg, err := deb.SourcePackage(ctx, sOpt, worker.With(ensureGolang(client, spec, targetKey, opts...)), spec, targetKey, versionID, opts...)
	if err != nil {
		return worker, err
	}

	builder := worker.With(dalec.SetBuildNetworkMode(spec))

	st, err := deb.BuildDeb(builder, spec, srcPkg, versionID, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
}

// ensureGolang is a work-around for the case where the base distro golang package
// is too old, but other packages are provided (e.g. `golang-1.22`) and those
// other packages don't actually add go tools to $PATH.
// It assumes if you added one of these go packages and there is no `go` in $PATH
// that you probably wanted to use that version of go.
func ensureGolang(client gwclient.Client, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		deps := spec.GetBuildDeps(targetKey)
		if _, hasNormalGo := deps["golang"]; hasNormalGo {
			return in
		}

		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			var candidates []string
			for dep := range deps {
				if strings.HasPrefix(dep, "golang-") {
					// Get the base version component
					_, ver, _ := strings.Cut(dep, "-")
					// Trim off any potential extra stuff like `golang-1.20-go` (ie the `-go` bit)
					// This is just for having definitive search paths to check it should
					// not be an issue if this is not like the above example and its
					// something else like `-doc` since we are still going to check the
					// binary exists anyway (plus this would be highly unlikely in any case).
					ver, _, _ = strings.Cut(ver, "-")
					candidates = append(candidates, "usr/lib/go-"+ver+"/bin")
				}
			}

			if len(candidates) == 0 {
				return in, nil
			}

			opts := []llb.ConstraintsOpt{dalec.WithConstraint(c), dalec.WithConstraints(opts...)}

			pathVar, _, err := in.GetEnv(ctx, "PATH", opts...)
			if err != nil {
				return in, err
			}

			stfs, err := bkfs.FromState(ctx, &in, client, opts...)
			if err != nil {
				return in, err
			}

			for _, p := range candidates {
				_, err := fs.Stat(stfs, filepath.Join(p, "go"))
				if err == nil {
					// bkfs does not allow a leading `/` in the stat path per spec for [fs.FS]
					// Add that in here
					p := "/" + p
					return in.AddEnv("PATH", p+":"+pathVar), nil
				}
			}
			return in, nil
		})
	}
}

func (cfg *Config) HandleDeb(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)

		worker, err := cfg.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		st, err := cfg.BuildDeb(ctx, worker, sOpt, client, spec, targetKey, pg)
		if err != nil {
			return nil, nil, err
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}

		if err := ref.Evaluate(ctx); err != nil {
			return ref, nil, err
		}

		ctr, err := cfg.BuildContainer(worker, sOpt, client, spec, targetKey, st, pg)
		if err != nil {
			return ref, nil, err
		}

		if ref, err := cfg.runTests(ctx, client, spec, sOpt, targetKey, ctr, pg); err != nil {
			cfg, _ := cfg.BuildImageConfig(ctx, client, spec, platform, targetKey)
			return ref, cfg, err
		}

		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}
		return ref, &dalec.DockerImageSpec{Image: ocispecs.Image{Platform: *platform}}, nil
	})
}

func (cfg *Config) runTests(ctx context.Context, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, ctr llb.State, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
	def, err := ctr.Marshal(ctx, opts...)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	withTestDeps := cfg.InstallTestDeps(sOpt, targetKey, spec, opts...)
	err = frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey)
	return ref, err
}

func (cfg *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)

		worker, err := cfg.Worker(sOpt, pg)
		if err != nil {
			return nil, nil, err
		}

		versionID, err := deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return nil, nil, err
		}

		worker = worker.With(cfg.InstallBuildDeps(sOpt, spec, targetKey, pg))
		st, err := deb.SourcePackage(ctx, sOpt, worker.With(ensureGolang(client, spec, targetKey, pg)), spec, targetKey, versionID, pg)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error building source package")
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, errors.Wrap(err, "error marshalling source package state")
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}

		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}
		return ref, nil, nil
	})
}
