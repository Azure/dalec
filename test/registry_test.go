package test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sync"
	"testing"

	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/container/containerapi"
	dockerimage "github.com/cpuguy83/go-docker/image"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

var (
	registryHostOnce sync.Once
	regHost          string
	regRelease       func(context.Context) error
	regErr           error
)

func registryHost(ctx context.Context, t *testing.T) string {
	registryHostOnce.Do(func() {
		if buildxContainerID != "" {
			regHost, regRelease, regErr = _setupRegistry(ctx)
		}
	})

	if regErr != nil {
		t.Fatal(regErr)
	}
	return regHost
}

// _setupRegistry is used to setup a local registry for testing.
// It returns the host:port of the registry and a function to cleanup the registry.
// Do not call this directly, use [registryHost] instead.
func _setupRegistry(ctx context.Context) (host string, release func(context.Context) error, retErr error) {
	ctx, span := otel.Tracer("").Start(ctx, "setupRegistry")
	defer func() {
		if retErr != nil {
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.SetAttributes(attribute.String("host", host))
		span.End()
	}()

	dc := docker.NewClient(docker.WithTransport(dockerTransport))
	remote, err := dockerimage.ParseRef("registry:2")
	if err != nil {
		return "", nil, err
	}
	if err := dc.ImageService().Pull(ctx, remote); err != nil {
		return "", nil, err
	}

	var regHost string
	containers := dc.ContainerService()
	ctr, err := containers.Create(ctx, "registry:2", func(cfg *container.CreateConfig) {
		// If this is a buildx container we put the registry into the same network namespace as the buildx container.
		// This is so we don't need to expose the registry on a port on the host that is not localhost.
		if buildxContainerID != "" {
			inspect, err := containers.Inspect(ctx, buildxContainerID)
			if err == nil && inspect.HostConfig.NetworkMode != "host" {
				cfg.Spec.HostConfig.NetworkMode = "container:" + buildxContainerID
				regHost = "127.0.0.1:5000"
				return
			}
			// In this case the container is using host mode networking, in which case we need to expose the registry on the host.
		}
		cfg.Spec.HostConfig.PortBindings = containerapi.PortMap{
			"5000/tcp": []containerapi.PortBinding{{HostIP: "127.0.0.1"}},
		}
	})
	if err != nil {
		return "", nil, err
	}

	release = func(ctx context.Context) error {
		return containers.Remove(ctx, ctr.ID(), container.WithRemoveForce)
	}
	defer func() {
		if retErr != nil {
			err := release(context.WithoutCancel(ctx))
			if err != nil {
				retErr = stderrors.Join(retErr, err)
			}
		}
	}()

	if err := ctr.Start(ctx); err != nil {
		return "", nil, err
	}

	if regHost != "" {
		return regHost, release, nil
	}

	inspect, err := ctr.Inspect(ctx)
	if err != nil {
		return "", nil, err
	}
	port := inspect.NetworkSettings.Ports["5000/tcp"][0].HostPort
	regHost = "127.0.0.1:" + port

	return regHost, release, nil
}

func setRegistryExport(ref string, so *client.SolveOpt) {
	so.Exports = []client.ExportEntry{
		{
			Type: func() string {
				if buildxContainerID != "" {
					return "image"
				}
				return "moby"
			}(),
			Attrs: map[string]string{
				"name": ref,
				"push": func() string {
					if buildxContainerID != "" {
						return "true"
					}
					return "false"
				}(),
			},
		},
	}
}

// buildFrontendImage builds the local frontend and pushes it to a registry or to docker, depending on what buildx builder is configured.
// If the default builder is selected (which is determined when setting up the buildkit client), then it will just add the frontend to the local docker daemon.
// Otherwise it needs to push it to a registry so that buildkit can fetch it accordingly.
//
// It returns the image name and a function to cleanup the image/registry.
func buildFrontendImage(ctx context.Context, c *client.Client, t *testing.T) (_ string, retErr error) {
	imgName := makeFrontendRef(ctx, t, identity.NewID())

	if regHost == "" {
		// Image only lives in dockerd
		t.Cleanup(func() {
			images := docker.NewClient(docker.WithTransport(dockerTransport)).ImageService()
			_, err := images.Remove(ctx, imgName, func(config *dockerimage.ImageRemoveConfig) error {
				config.Force = true
				return nil
			})
			t.Log(err)
		})
	}

	var so client.SolveOpt
	setRegistryExport(imgName, &so)
	withProjectRoot(t, &so)

	eg, ctx := errgroup.WithContext(ctx)
	ch := displaySolveStatus(ctx, eg)
	_, err := c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		dc, err := dockerui.NewClient(gwc)
		if err != nil {
			return nil, err
		}

		rb, err := dc.Build(ctx, func(ctx context.Context, platform *v1.Platform, idx int) (gwclient.Reference, *image.Image, error) {
			res, err := buildLocalFrontend(ctx, gwc)
			if err != nil {
				return nil, nil, err
			}

			ref, err := res.SingleRef()
			if err != nil {
				return nil, nil, err
			}

			dt := res.Metadata[exptypes.ExporterImageConfigKey]

			var img image.Image
			if dt != nil {
				if err := json.Unmarshal(dt, &img); err != nil {
					return nil, nil, err
				}
			}
			return ref, &img, nil
		})
		if err != nil {
			return nil, errors.Wrap(err, "error building local frontend")
		}
		return rb.Finalize()
	}, ch)

	if err != nil {
		return "", err
	}

	return imgName, nil
}
