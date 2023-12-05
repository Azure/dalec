package test

import (
	"context"
	"encoding/json"
	stderrs "errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cpuguy83/dockercfg"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/util/tracing/detect"
	"github.com/pkg/errors"
)

var (
	dockerTransport   transport.Doer
	buildxContainerID string
	baseClient        *client.Client
)

func defaultBuildkitClient(ctx context.Context) (*client.Client, error) {
	p, err := dockercfg.ConfigPath()
	if err != nil {
		return nil, err
	}

	builder := os.Getenv("BUILDX_BUILDER")

	configBase := filepath.Join(filepath.Dir(p), "buildx")

	var opts []client.ClientOpt

	tp, err := detect.TracerProvider()
	if err != nil {
		return nil, err
	}
	opts = append(opts, client.WithTracerProvider(tp))

	exp, err := detect.Exporter()
	if err != nil {
		return nil, err
	}
	if delegate, ok := exp.(client.TracerDelegate); ok {
		opts = append(opts, client.WithTracerDelegate(delegate))
	}

	// Find a buildkit instance to connect to via buildx.
	// This only supports either the "default" builder (aka docker) or builders using the "docker-container" driver.
	// It also will only ever select the currently active builder (or the one set by the standard BUILDX_BUILDER env var).
	if builder == "" {
		dt, err := os.ReadFile(filepath.Join(configBase, "current"))
		if err != nil {
			if os.IsNotExist(err) {
				tr, err := transport.DefaultTransport()
				if err != nil {
					return nil, errors.Wrap(err, "failed to get default docker transport")
				}
				dockerTransport = tr
				return client.New(ctx, "", append(opts, buildkitopt.FromDocker(tr)...)...)
			}
			return nil, err
		}

		type ref struct {
			Name string
			Key  string
		}
		var r ref
		if err := json.Unmarshal(dt, &r); err != nil {
			return nil, err
		}

		if r.Name == "" {
			var tr transport.Doer
			if r.Key != "" {
				tr, err = transport.FromConnectionString(r.Key)
				if err != nil {
					return nil, err
				}
				dockerTransport = tr
			} else {
				tr, err = transport.DefaultTransport()
				if err != nil {
					return nil, err
				}
				dockerTransport = tr
			}
			return client.New(ctx, "", append(opts, buildkitopt.FromDocker(tr)...)...)
		}

		builder = r.Name
	}

	if out, err := exec.Command("docker", "buildx", "inspect", "--bootstrap", builder).CombinedOutput(); err != nil {
		return nil, errors.Wrapf(err, "failed to bootstrap builder: %s", out)
	}

	dt, err := os.ReadFile(filepath.Join(configBase, "instances", builder))
	if err != nil {
		return nil, errors.Wrap(err, "failed to read buildx instance config")
	}

	var cfg buildxConfig
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal buildx config")
	}

	if cfg.Driver != "docker-container" {
		return nil, errors.Errorf("unsupported buildx driver: %s", cfg.Driver)
	}

	if len(cfg.Nodes) == 0 {
		return nil, errors.New("no nodes available for configured buildx builder")
	}

	nodes := cfg.Nodes
	if len(nodes) > 1 {
		rand.Shuffle(len(nodes), func(i, j int) {
			nodes[i], nodes[j] = nodes[j], nodes[i]
		})
	}

	var errs []error
	for _, n := range nodes {
		tr, err := transport.FromConnectionString(n.Endpoint)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Endpoint, err))
			continue
		}

		dc := docker.NewClient(docker.WithTransport(tr))
		ctr := dc.ContainerService().NewContainer(ctx, "buildx_buildkit_"+n.Name)

		conn1, conn2 := net.Pipe()
		ep, err := ctr.Exec(ctx, container.WithExecCmd("buildctl", "dial-stdio"), func(cfg *container.ExecConfig) {
			cfg.Stdin = conn1
			cfg.Stdout = conn1
			cfg.Stderr = conn1
		})
		if err != nil {
			conn1.Close()
			conn2.Close()
			errs = append(errs, fmt.Errorf("%s: %w", n.Endpoint, err))
			continue
		}

		if err := ep.Start(ctx); err != nil {
			conn1.Close()
			conn2.Close()
			errs = append(errs, fmt.Errorf("%s: %w", n.Endpoint, err))
			continue
		}

		opts := append(opts, client.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return conn2, nil
		}))
		c, err := client.New(ctx, "", opts...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Endpoint, err))
			continue
		}
		buildxContainerID = ctr.ID()
		return c, nil
	}

	return nil, stderrs.Join(errs...)
}

type buildxConfig struct {
	Driver string
	Nodes  []struct {
		Name     string
		Endpoint string
	}
}

// supportsFrontendNamedContexts checks if we can overwrite the frontend ref via named contexts.
//
// More info:
// Buildkit treats the frontend ref (`#syntax=<ref>` or via the BUILDKIT_SYNTAX
// var) as a docker image ref.
// Buildkit will always check the remote registry for a new version of the image.
// As of buildkit v0.12 you can use named contexts to ovewrite the frontend ref
// with another type of ref.
// This can be another docker-image, an oci-layout, or even a frontend "input"
// (like feeding the output of a build into another build).
// Here we are checking the version of buildkit to determine what method we can
// use.
// In the future we'll just drop this version check and always use named
// contexts, but for now we need to be able to run the test suite against older
// versions of buildkit, where "older" means the version currently shipping with Docker (in v24).
func supportsFrontendNamedContexts(ctx context.Context, client *client.Client) bool {
	info, err := client.Info(ctx)
	if err != nil {
		return false
	}
	majorStr, minorPatchStr, ok := strings.Cut(strings.TrimPrefix(info.BuildkitVersion.Version, "v"), ".")
	if !ok {
		return false
	}

	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return false
	}

	if major > 0 {
		return true
	}

	minorStr, _, _ := strings.Cut(minorPatchStr, ".")

	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return false
	}

	return major > 0 || minor >= 12
}
