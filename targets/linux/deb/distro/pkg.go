package distro

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/pkg/bkfs"
	"github.com/Azure/dalec/packaging/linux/deb"
	"github.com/Azure/dalec/targets"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func (d *Config) Validate(spec *dalec.Spec) error {
	return nil
}

func (d *Config) BuildPkg(ctx context.Context, client gwclient.Client, worker llb.State, sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Build deb package"))

	versionID := d.VersionID
	if versionID == "" {
		var err error
		versionID, err = deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return worker, err
		}
	}

	worker = worker.With(d.InstallBuildDeps(sOpt, spec, targetKey, append(opts, frontend.IgnoreCache(client))...))

	var cfg deb.SourcePkgConfig
	extraPaths, err := prepareGo(ctx, client, &cfg, worker, spec, targetKey, opts...)
	if err != nil {
		return worker, err
	}

	cargoExtraPaths, err := prepareCargo(ctx, client, &cfg, worker, spec, targetKey, opts...)
	if err != nil {
		return worker, err
	}

	// Combine the extra paths from both Go and Cargo preparations
	combinedExtraPaths := func(in llb.State) llb.State {
		return cargoExtraPaths(extraPaths(in))
	}

	srcPkg, err := deb.SourcePackage(ctx, sOpt, worker.With(combinedExtraPaths), spec, targetKey, versionID, cfg, append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeySrcPkg))...)
	if err != nil {
		return worker, err
	}

	builder := worker.With(dalec.SetBuildNetworkMode(spec))

	st, err := deb.BuildDeb(builder, spec, srcPkg, versionID, append(opts, frontend.IgnoreCache(client, targets.IgnoreCacheKeyPkg))...)
	if err != nil {
		return llb.Scratch(), err
	}

	// Filter out everything except the .deb files
	filtered := llb.Scratch().File(
		llb.Copy(st, "/", "/",
			dalec.WithIncludes([]string{"**/*.deb"}),
		),
	)

	signed, err := frontend.MaybeSign(ctx, client, filtered, spec, targetKey, sOpt, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	// Merge the signed state with the original state
	// The signed files should overwrite the unsigned ones.
	st = st.File(llb.Copy(signed, "/", "/"))
	return st, nil
}

func noOpStateOpt(in llb.State) llb.State {
	return in
}

func prepareGo(ctx context.Context, client gwclient.Client, cfg *deb.SourcePkgConfig, worker llb.State, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	if !dalec.HasGolang(spec, targetKey) && !spec.HasGomods() {
		return noOpStateOpt, nil
	}

	addGoCache := true
	for _, c := range spec.Build.Caches {
		if c.GoBuild != nil {
			addGoCache = false
		}
	}

	if addGoCache {
		spec.Build.Caches = append(spec.Build.Caches, dalec.CacheConfig{
			GoBuild: &dalec.GoBuildCache{},
		})
	}

	goBin, err := searchForAltGolang(ctx, client, spec, targetKey, worker, opts...)
	if err != nil {
		return noOpStateOpt, errors.Wrap(err, "error while looking for alternate go bin path")
	}

	if goBin == "" {
		return noOpStateOpt, nil
	}
	cfg.PrependPath = append(cfg.PrependPath, goBin)
	return addPaths([]string{goBin}, opts...), nil
}

func searchForAltGolang(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string, in llb.State, opts ...llb.ConstraintsOpt) (string, error) {
	if !spec.HasGomods() {
		return "", nil
	}

	deps := spec.GetBuildDeps(targetKey)
	if _, hasNormalGo := deps["golang"]; hasNormalGo {
		return "", nil
	}

	candidates := buildCandidatePaths(deps, "golang", "usr/lib/go", "/bin")
	return findBinaryInCandidates(ctx, client, in, candidates, "go", opts...)
}

func searchForAltRust(ctx context.Context, client gwclient.Client, spec *dalec.Spec, targetKey string, in llb.State, opts ...llb.ConstraintsOpt) (string, error) {
	if !spec.HasCargohomes() {
		return "", nil
	}

	deps := spec.GetBuildDeps(targetKey)
	if _, hasNormalRust := deps["rust"]; hasNormalRust {
		return "", nil
	}
	if _, hasNormalCargo := deps["cargo"]; hasNormalCargo {
		return "", nil
	}

	// Check for both rust- and cargo- prefixed packages
	rustCandidates := buildCandidatePaths(deps, "rust", "usr/lib/rust", "/bin")
	cargoCandidates := buildCandidatePaths(deps, "cargo", "usr/lib/cargo", "/bin")

	allCandidates := append(rustCandidates, cargoCandidates...)

	// Try to find cargo first, then rustc as fallback
	if path, err := findBinaryInCandidates(ctx, client, in, allCandidates, "cargo", opts...); err == nil && path != "" {
		return path, nil
	}
	return findBinaryInCandidates(ctx, client, in, allCandidates, "rustc", opts...)
}

// buildCandidatePaths extracts version-specific package paths from dependencies
func buildCandidatePaths(deps map[string]dalec.PackageConstraints, prefix, basePath, suffix string) []string {
	var candidates []string

	for dep := range deps {
		if strings.HasPrefix(dep, prefix+"-") {
			// Get the base version component
			_, ver, _ := strings.Cut(dep, "-")
			// Trim off any potential extra stuff like `golang-1.20-go` (ie the `-go` bit)
			// This is just for having definitive search paths to check it should
			// not be an issue if this is not like the above example and its
			// something else like `-doc` since we are still going to check the
			// binary exists anyway (plus this would be highly unlikely in any case).
			ver, _, _ = strings.Cut(ver, "-")
			candidates = append(candidates, basePath+"-"+ver+suffix)
		}
	}

	return candidates
}

// findBinaryInCandidates searches for a binary in a list of candidate paths
func findBinaryInCandidates(ctx context.Context, client gwclient.Client, in llb.State, candidates []string, binaryName string, opts ...llb.ConstraintsOpt) (string, error) {
	if len(candidates) == 0 {
		return "", nil
	}

	stfs, err := bkfs.FromState(ctx, &in, client, opts...)
	if err != nil {
		return "", err
	}

	for _, p := range candidates {
		_, err := fs.Stat(stfs, filepath.Join(p, binaryName))
		if err == nil {
			// bkfs does not allow a leading `/` in the stat path per spec for [fs.FS]
			// Add that in here
			p := "/" + p
			return p, nil
		}
	}

	return "", nil
}

func prepareCargo(ctx context.Context, client gwclient.Client, cfg *deb.SourcePkgConfig, worker llb.State, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.StateOption, error) {
	if !dalec.HasRust(spec, targetKey) && !spec.HasCargohomes() {
		return noOpStateOpt, nil
	}

	addCargoCache := true
	for _, c := range spec.Build.Caches {
		if c.CargoBuild != nil {
			addCargoCache = false
		}
	}

	if addCargoCache {
		spec.Build.Caches = append(spec.Build.Caches, dalec.CacheConfig{
			CargoBuild: &dalec.CargoBuildCache{},
		})
	}

	cargoBin, err := searchForAltRust(ctx, client, spec, targetKey, worker, opts...)
	if err != nil {
		return noOpStateOpt, errors.Wrap(err, "error while looking for alternate rust bin path")
	}

	if cargoBin == "" {
		return noOpStateOpt, nil
	}
	cfg.PrependPath = append(cfg.PrependPath, cargoBin)
	return addPaths([]string{cargoBin}, opts...), nil
}

// prepends the provided values to $PATH
func addPaths(paths []string, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(paths) == 0 {
			return in
		}
		return in.Async(func(ctx context.Context, in llb.State, c *llb.Constraints) (llb.State, error) {
			opts := []llb.ConstraintsOpt{dalec.WithConstraint(c), dalec.WithConstraints(opts...)}
			pathEnv, _, err := in.GetEnv(ctx, "PATH", opts...)
			if err != nil {
				return in, err
			}
			return in.AddEnv("PATH", strings.Join(append(paths, pathEnv), ":")), nil
		})
	}
}

func (cfg *Config) RunTests(ctx context.Context, client gwclient.Client, _ llb.State, spec *dalec.Spec, sOpt dalec.SourceOpts, ctr llb.State, targetKey string, opts ...llb.ConstraintsOpt) (gwclient.Reference, error) {
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

	opts = append(opts, frontend.IgnoreCache(client))
	withTestDeps := cfg.InstallTestDeps(sOpt, targetKey, spec, opts...)
	err = frontend.RunTests(ctx, client, spec, ref, withTestDeps, targetKey, sOpt.TargetPlatform)
	return ref, err
}

func (cfg *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
		sOpt, err := frontend.SourceOptFromClient(ctx, client, platform)
		if err != nil {
			return nil, nil, err
		}

		pg := dalec.ProgressGroup(spec.Name)
		pc := dalec.Platform(platform)

		worker, err := cfg.Worker(sOpt, pg, pc, frontend.IgnoreCache(client, cfg.ImageRef, cfg.ContextRef))
		if err != nil {
			return nil, nil, err
		}

		versionID, err := deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return nil, nil, err
		}

		worker = worker.With(cfg.InstallBuildDeps(sOpt, spec, targetKey, pg, frontend.IgnoreCache(client)))

		var cfg deb.SourcePkgConfig
		extraPaths, err := prepareGo(ctx, client, &cfg, worker, spec, targetKey, pg, frontend.IgnoreCache(client))
		if err != nil {
			return nil, nil, err
		}

		cargoExtraPaths, err := prepareCargo(ctx, client, &cfg, worker, spec, targetKey, pg, frontend.IgnoreCache(client))
		if err != nil {
			return nil, nil, err
		}

		// Combine the extra paths from both Go and Cargo preparations
		combinedExtraPaths := func(in llb.State) llb.State {
			return cargoExtraPaths(extraPaths(in))
		}

		st, err := deb.SourcePackage(ctx, sOpt, worker.With(combinedExtraPaths), spec, targetKey, versionID, cfg, pg, pc, frontend.IgnoreCache(client, targets.IgnoreCacheKeySrcPkg))
		if err != nil {
			return nil, nil, errors.Wrap(err, "error building source package")
		}

		def, err := st.Marshal(ctx, frontend.IgnoreCache(client))
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
