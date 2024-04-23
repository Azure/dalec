package azlinux

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func handleDepsOnly(w worker) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		return frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
			pg := dalec.ProgressGroup("Build mariner2 deps-only container: " + spec.Name)
			baseImg := w.Base(client, pg)
			rpmDir := baseImg.Run(
				shArgs(`set -ex; dir="/tmp/rpms/RPMS/$(uname -m)"; mkdir -p "${dir}"; tdnf install -y --releasever=2.0 --downloadonly --alldeps --downloaddir "${dir}" `+strings.Join(spec.GetRuntimeDeps(targetKey), " ")),
				pg,
			).
				AddMount("/tmp/rpms", llb.Scratch())

			files, err := readRPMs(ctx, client, rpmDir)
			if err != nil {
				return nil, nil, err
			}

			sOpt, err := frontend.SourceOptFromClient(ctx, client)
			if err != nil {
				return nil, nil, err
			}
			st, err := specToContainerLLB(w, client, spec, targetKey, rpmDir, files, sOpt, pg)
			if err != nil {
				return nil, nil, err
			}

			def, err := st.Marshal(ctx, pg)
			if err != nil {
				return nil, nil, err
			}

			res, err := client.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if err != nil {
				return nil, nil, err
			}

			var img *dalec.DockerImageSpec
			if base := frontend.GetBaseOutputImage(spec, targetKey, ""); base != "" {
				_, _, dt, err := client.ResolveImageConfig(ctx, base, sourceresolver.Opt{})
				if err != nil {
					return nil, nil, errors.Wrap(err, "error resolving base image config")
				}
				var cfg dalec.DockerImageSpec
				if err := json.Unmarshal(dt, &cfg); err != nil {
					return nil, nil, errors.Wrap(err, "error unmarshalling base image config")
				}
				img = &cfg
			} else {
				img, err = w.DefaultImageConfig(ctx, client)
				if err != nil {
					return nil, nil, errors.Wrap(err, "could not get default image config")
				}
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, nil, err
			}

			return ref, img, nil
		})
	}
}
