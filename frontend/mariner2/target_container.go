package mariner2

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

const (
	marinerDistrolessRef = "mcr.microsoft.com/cbl-mariner/distroless/base:2.0"
)

func handleContainer(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error) {
	baseImg, err := getBaseBuilderImg(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	sOpt, err := frontend.SourceOptFromClient(ctx, client)
	if err != nil {
		return nil, nil, err
	}

	st, err := specToContainerLLB(spec, targetKey, getDigestFromClientFn(ctx, client), baseImg, sOpt)
	if err != nil {
		return nil, nil, err
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, nil, err
	}

	_, _, dt, err := client.ResolveImageConfig(ctx, marinerRef, llb.ResolveImageConfigOpt{})
	if err != nil {
		return nil, nil, fmt.Errorf("error resolving image config: %w", err)
	}

	var img image.Image
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, nil, fmt.Errorf("error unmarshalling image config: %w", err)
	}

	copyImageConfig(&img, spec.Targets[targetKey].Image)

	ref, err := res.SingleRef()
	return ref, &img, err
}

func specToContainerLLB(spec *dalec.Spec, target string, getDigest getDigestFunc, builderImg llb.State, sOpt dalec.SourceOpts) (llb.State, error) {
	st, err := specToRpmLLB(spec, getDigest, builderImg, sOpt)
	if err != nil {
		return llb.Scratch(), fmt.Errorf("error creating rpm: %w", err)
	}

	const workPath = "/tmp/rootfs"

	baseRef := marinerDistrolessRef
	if spec.Targets[target].Image != nil && spec.Targets[target].Image.Base != "" {
		baseRef = spec.Targets[target].Image.Base
	}

	baseImg := llb.Image(baseRef, llb.WithMetaResolver(sOpt.Resolver))
	mfstDir := filepath.Join(workPath, "var/lib/rpmmanifest")
	mfst1 := filepath.Join(mfstDir, "container-manifest-1")
	mfst2 := filepath.Join(mfstDir, "container-manifest-2")
	rpmdbDir := filepath.Join(workPath, "var/lib/rpm")

	chrootedPaths := []string{
		filepath.Join(workPath, "/usr/local/bin"),
		filepath.Join(workPath, "/usr/local/sbin"),
		filepath.Join(workPath, "/usr/bin"),
		filepath.Join(workPath, "/usr/sbin"),
		filepath.Join(workPath, "/bin"),
		filepath.Join(workPath, "/sbin"),
	}
	chrootedPathEnv := strings.Join(chrootedPaths, ":")

	mfstCmd := `
# If the rpm command is in the rootfs then we don't need to do anything
# If not then this is a distroless image and we need to generate manifests of the installed rpms and cleanup the rpmdb.

PATH="` + chrootedPathEnv + `" command -v rpm && exit 0

set -e
mkdir -p ` + mfstDir + `
rpm --dbpath=` + rpmdbDir + ` -qa > ` + mfst1 + `
rpm --dbpath=` + rpmdbDir + ` -qa --qf "%{NAME}\t%{VERSION}-%{RELEASE}\t%{INSTALLTIME}\t%{BUILDTIME}\t%{VENDOR}\t(none)\t%{SIZE}\t%{ARCH}\t%{EPOCHNUM}\t%{SOURCERPM}\n" > ` + mfst2 + `
rm -rf ` + rpmdbDir + `
`

	workSt := builderImg.
		File(llb.Copy(baseImg, "/", workPath, dalec.WithDirContentsOnly(), dalec.WithCreateDestPath())).
		Run(
			shArgs("tdnf -v install --releasever=2.0 -y --nogpgcheck --installroot "+workPath+" --setopt=reposdir=/etc/yum.repos.d /tmp/rpms/$(uname -m)/*.rpm"),
			marinerTdnfCache,
			llb.AddMount("/tmp/rpms", st, llb.SourcePath("/RPMS")),
		).
		Run(shArgs(mfstCmd)).
		State

	// Flatten our changes to a single layer.
	diff := llb.Diff(baseImg, llb.Scratch().File(llb.Copy(workSt, workPath, "/", dalec.WithDirContentsOnly())))
	return llb.Merge([]llb.State{baseImg, diff}), nil
}

func copyImageConfig(dst *image.Image, src *dalec.ImageConfig) {
	if src == nil {
		return
	}

	if src.Entrypoint != nil {
		dst.Config.Entrypoint = src.Entrypoint
		// Reset cmd as this may be totally invalid now
		// This is the same behavior as the Dockerfile frontend
		dst.Config.Cmd = nil
	}
	if src.Cmd != nil {
		dst.Config.Cmd = src.Cmd
	}

	if len(src.Env) > 0 {
		// Env is append only
		// If the env var already exists, replace it
		envIdx := make(map[string]int)
		for i, env := range dst.Config.Env {
			envIdx[env] = i
		}

		for _, env := range src.Env {
			if idx, ok := envIdx[env]; ok {
				dst.Config.Env[idx] = env
			} else {
				dst.Config.Env = append(dst.Config.Env, env)
			}
		}
	}

	if src.WorkingDir != "" {
		dst.Config.WorkingDir = src.WorkingDir
	}
	if src.StopSignal != "" {
		dst.Config.StopSignal = src.StopSignal
	}
}
