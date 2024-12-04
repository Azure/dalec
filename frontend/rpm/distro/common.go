package distro

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func importGPGScript(keyPaths []string) string {
	// all keys that are included should be mounted under this path
	keyRoot := "/etc/pki/rpm-gpg"

	var importScript string = "#!/usr/bin/env sh\nset -eux\n"
	for _, keyPath := range keyPaths {
		keyName := filepath.Base(keyPath)
		importScript += fmt.Sprintf("gpg --import %s\n", filepath.Join(keyRoot, keyName))
	}

	return importScript
}

func specToRpmLLB(ctx context.Context, base llb.State, client gwclient.Client, spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	installOpt, err := installBuildDeps(ctx, w, client, spec, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}
	base = base.With(installOpt)

	br, err := rpm.SpecToBuildrootLLB(base, spec, sOpt, targetKey, opts...)
	if err != nil {
		return llb.Scratch(), err
	}

	specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")

	builder := base.With(dalec.SetBuildNetworkMode(spec))
	st := rpm.Build(br, builder, specPath, opts...)

	return frontend.MaybeSign(ctx, client, st, spec, targetKey, sOpt, opts...)
}
