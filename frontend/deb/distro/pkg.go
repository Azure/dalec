package distro

import (
	"context"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/deb"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func (d *Config) BuildDeb(ctx context.Context, sOpt dalec.SourceOpts, client gwclient.Client, spec *dalec.Spec, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	opts = append(opts, dalec.ProgressGroup("Build deb package"))

	worker, err := d.Worker(sOpt, opts...)
	if err != nil {
		return llb.Scratch(), nil
	}

	versionID := d.VersionID
	if versionID == "" {
		versionID, err = deb.ReadDistroVersionID(ctx, client, worker)
		if err != nil {
			return worker, err
		}
	}

	worker = worker.With(d.InstallBuildDeps(sOpt, spec, targetKey))
	srcPkg, err := deb.SourcePackage(sOpt, worker, spec, targetKey, versionID, opts...)
	if err != nil {
		return worker, err
	}

	builder := worker.With(dalec.SetBuildNetworkMode(spec))

	st, err := deb.BuildDeb(builder, spec, srcPkg, versionID, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	signed, err := frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt)
	if err != nil {
		return st, err
	}
	return signed, nil
}
