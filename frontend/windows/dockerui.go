package windows

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
)

// This is a copy of dockerui.Client.Build
// It has one modification: Instead of `platforms.Format` it uses `platforms.FormatAll`
// The value returned from this function is used as a map key to store build
// result references.
// When `platforms.Format` is used, the `OSVersion` field is not taken into account
// which means we end up overwriting map keys when there are multiple windows
// platform images being output but with different OSVersions.
// platforms.FormatAll takes OSVersion into account.
func dcBuild(ctx context.Context, bc *dockerui.Client, fn dockerui.BuildFunc) (*resultBuilder, error) {
	res := gwclient.NewResult()

	targets := make([]*ocispecs.Platform, 0, len(bc.TargetPlatforms))
	for _, p := range bc.TargetPlatforms {
		p := p
		targets = append(targets, &p)
	}
	if len(targets) == 0 {
		targets = append(targets, nil)
	}
	expPlatforms := &exptypes.Platforms{
		Platforms: make([]exptypes.Platform, len(targets)),
	}

	eg, ctx := errgroup.WithContext(ctx)

	for i, tp := range targets {
		i, tp := i, tp
		eg.Go(func() error {
			ref, img, baseImg, err := fn(ctx, tp, i)
			if err != nil {
				return err
			}

			config, err := json.Marshal(img)
			if err != nil {
				return errors.Wrapf(err, "failed to marshal image config")
			}

			var baseConfig []byte
			if baseImg != nil {
				baseConfig, err = json.Marshal(baseImg)
				if err != nil {
					return errors.Wrapf(err, "failed to marshal source image config")
				}
			}

			p := platforms.DefaultSpec()
			if tp != nil {
				p = *tp
			}

			// in certain conditions we allow input platform to be extended from base image
			if p.OS == "windows" && img.OS == p.OS {
				if p.OSVersion == "" && img.OSVersion != "" {
					p.OSVersion = img.OSVersion
				}
				if p.OSFeatures == nil && len(img.OSFeatures) > 0 {
					p.OSFeatures = append([]string{}, img.OSFeatures...)
				}
			}

			p = platforms.Normalize(p)
			k := platforms.FormatAll(p)

			if bc.MultiPlatformRequested {
				res.AddRef(k, ref)
				res.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, k), config)
				if len(baseConfig) > 0 {
					res.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageBaseConfigKey, k), baseConfig)
				}
			} else {
				res.SetRef(ref)
				res.AddMeta(exptypes.ExporterImageConfigKey, config)
				if len(baseConfig) > 0 {
					res.AddMeta(exptypes.ExporterImageBaseConfigKey, baseConfig)
				}
			}
			expPlatforms.Platforms[i] = exptypes.Platform{
				ID:       k,
				Platform: p,
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return &resultBuilder{
		Result:       res,
		expPlatforms: expPlatforms,
	}, nil
}

type resultBuilder struct {
	*gwclient.Result
	expPlatforms *exptypes.Platforms
}

func (rb *resultBuilder) Finalize() (*gwclient.Result, error) {
	dt, err := json.Marshal(rb.expPlatforms)
	if err != nil {
		return nil, err
	}
	rb.AddMeta(exptypes.ExporterPlatformsKey, dt)

	return rb.Result, nil
}
