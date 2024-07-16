package azlinux

import (
	"context"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func installWithReqs(w worker, deps map[string]dalec.PackageConstraints, installOpts ...installOpt) installFunc {
	// depsOnly is a simple dalec spec that only includes build dependencies and their constraints
	depsOnly := dalec.Spec{
		Name:        "azlinux-build-dependencies",
		Description: "Provides build dependencies for mariner2 and azlinux3",
		Version:     "1.0",
		License:     "Apache 2.0",
		Revision:    "1",
		Dependencies: &dalec.PackageDependencies{
			Runtime: deps,
		},
	}

	return func(ctx context.Context, client gwclient.Client, sOpt dalec.SourceOpts) (llb.RunOption, error) {
		pg := dalec.ProgressGroup("Building container for build dependencies")

		// create an RPM with just the build dependencies, using our same base worker
		rpmDir, err := specToRpmLLB(ctx, w, client, &depsOnly, sOpt, "mariner2", pg)
		if err != nil {
			return nil, err
		}

		// read the built RPMs (there should be a single one)
		rpms, err := readRPMs(ctx, client, rpmDir)
		if err != nil {
			return nil, err
		}

		var opts []llb.ConstraintsOpt
		opts = append(opts, dalec.ProgressGroup("Install build deps"))

		rpmMountDir := "/tmp/rpms"
		fullRPMPaths := make([]string, 0, len(rpms))
		for _, rpm := range rpms {
			fullRPMPaths = append(fullRPMPaths, filepath.Join(rpmMountDir, rpm))
		}

		installOpts = append([]installOpt{
			noGPGCheck,
			withMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"))),
			installWithConstraints(opts),
		}, installOpts...)

		// install the RPM into the worker itself, using the same base worker
		return w.Install(fullRPMPaths, installOpts...), nil
	}
}
