package azlinux

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/Azure/dalec/frontend/rpm"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func binCopyScript(rpms []string, binaries map[string]dalec.ArtifactConfig) string {
	var sb strings.Builder

	sb.WriteString(`
#!/bin/sh
set -e
declare -a RPMS=()
RPM_BINDIR=$(rpm --eval '%{_bindir}')
`)

	for _, rpm := range rpms {
		sb.WriteString(fmt.Sprintf("RPMS+=(%q)\n", rpm))
	}

	sb.WriteString("for rpm in $RPMS; do\n")
	for path, bin := range binaries {
		baseName := filepath.Base(path)
		if bin.Name != "" {
			baseName = bin.Name
		}
		srcPath := filepath.Join(bin.SubPath, baseName)

		sb.WriteString(fmt.Sprintf("rpm2cpio /package/RPMS/$rpm | cpio -imvd $RPM_BINDIR/%s\n", srcPath))
	}
	sb.WriteString("done\n")

	return sb.String()
}

func handleBin(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			if err := rpm.ValidateSpec(spec); err != nil {
				return nil, nil, fmt.Errorf("rpm: invalid spec: %w", err)
			}
			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}

			pg := dalec.ProgressGroup("Building azlinux rpm: " + spec.Name)
			rpmState, err := specToRpmLLB(ctx, w, client, spec, sOpt, targetKey, pg)
			if err != nil {
				return nil, nil, err
			}

			rpms, err := readRPMs(ctx, client, rpmState)
			if err != nil {
				return nil, nil, err
			}

			pg = dalec.ProgressGroup("Extracting rpm binary artifacts: ")
			script := binCopyScript(rpms, spec.Artifacts.Binaries)
			scriptState := llb.Scratch().File(llb.Mkfile("/bin_copy.sh", 0755, []byte(script)), pg)

			st := w.Base(client, pg).Run(
				shArgs("/script/bin_copy.sh"),
				llb.AddMount("/script", scriptState),
				llb.AddMount("/package", rpmState),
			).Root()

			def, err := st.Marshal(ctx, pg)
			if err != nil {
				return nil, nil, fmt.Errorf("error marshalling llb: %w", err)
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
}
