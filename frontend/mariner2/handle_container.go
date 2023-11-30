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
	"github.com/moby/buildkit/frontend/dockerui"
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

	rpmDir, err := specToRpmLLB(spec, getDigestFromClientFn(ctx, client), baseImg, sOpt)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating rpm: %w", err)
	}

	st, err := specToContainerLLB(spec, targetKey, baseImg, rpmDir, sOpt)
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

	img, err := buildImageConfig(ctx, spec, targetKey, client)
	if err != nil {
		return nil, nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}

	if err := frontend.RunTests(ctx, client, spec, ref, targetKey); err != nil {
		return nil, nil, err
	}

	return ref, img, err
}

func buildImageConfig(ctx context.Context, spec *dalec.Spec, target string, client gwclient.Client) (*image.Image, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	baseImgRef := getBaseOutputImage(spec, targetKey)
	_, _, dt, err := client.ResolveImageConfig(ctx, baseImgRef, llb.ResolveImageConfigOpt{
		ResolveMode: dc.ImageResolveMode.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("error resolving image config: %w", err)
	}

	var img image.Image
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, fmt.Errorf("error unmarshalling image config: %w", err)
	}

	if err := frontend.CopyImageConfig(&img, dalec.MergeSpecImage(spec, targetKey)); err != nil {
		return nil, err
	}

	return &img, nil
}

func getBaseOutputImage(spec *dalec.Spec, target string) string {
	baseRef := marinerDistrolessRef
	if spec.Targets[target].Image != nil && spec.Targets[target].Image.Base != "" {
		baseRef = spec.Targets[target].Image.Base
	}
	return baseRef
}

func specToContainerLLB(spec *dalec.Spec, target string, builderImg llb.State, rpmDir llb.State, sOpt dalec.SourceOpts) (llb.State, error) {

	const workPath = "/tmp/rootfs"

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

	installCmd := `
#!/usr/bin/env sh

check_non_empty() {
	ls ${1} > /dev/null 2>&1
}

arch_dir="/tmp/rpms/$(uname -m)"
noarch_dir="/tmp/rpms/noarch"

rpms=""

if check_non_empty "${noarch_dir}/*.rpm"; then
	rpms="${noarch_dir}/*.rpm"
fi

if check_non_empty "${arch_dir}/*.rpm"; then
	rpms="${rpms} ${arch_dir}/*.rpm"
fi

if [ -n "${rpms}" ]; then
	tdnf -v install --releasever=2.0 -y --nogpgcheck --installroot "` + workPath + `" --setopt=reposdir=/etc/yum.repos.d ${rpms} || exit
fi

# If the rpm command is in the rootfs then we don't need to do anything
# If not then this is a distroless image and we need to generate manifests of the installed rpms and cleanup the rpmdb.

PATH="` + chrootedPathEnv + `" command -v rpm && exit 0

set -e
mkdir -p ` + mfstDir + `
rpm --dbpath=` + rpmdbDir + ` -qa > ` + mfst1 + `
rpm --dbpath=` + rpmdbDir + ` -qa --qf "%{NAME}\t%{VERSION}-%{RELEASE}\t%{INSTALLTIME}\t%{BUILDTIME}\t%{VENDOR}\t(none)\t%{SIZE}\t%{ARCH}\t%{EPOCHNUM}\t%{SOURCERPM}\n" > ` + mfst2 + `
rm -rf ` + rpmdbDir + `
`

	installer := llb.Scratch().File(llb.Mkfile("install.sh", 0o755, []byte(installCmd)))

	baseImg := llb.Image(getBaseOutputImage(spec, target), llb.WithMetaResolver(sOpt.Resolver))
	worker := builderImg.
		Run(
			shArgs("/tmp/install.sh"),
			marinerTdnfCache,
			llb.AddMount("/tmp/rpms", rpmDir, llb.SourcePath("/RPMS")),
			llb.AddMount("/tmp/install.sh", installer, llb.SourcePath("install.sh")),
		)

	// This adds a mount to the worker so that all the commands are run with this mount added
	// The return value is the state representing the contents of the mounted directory after the commands are run
	rootfs := worker.AddMount(workPath, baseImg)

	return rootfs, nil
}
