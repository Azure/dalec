package testenv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cpuguy83/dockercfg"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/moby/buildkit/client"
	pkgerrors "github.com/pkg/errors"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

type BuildxEnv struct {
	builder string

	mu     sync.Mutex
	client *client.Client

	ctr    *container.Container
	docker *docker.Client

	supportedOnce sync.Once
	supportedErr  error
}

func New() *BuildxEnv {
	return &BuildxEnv{}
}

func (b *BuildxEnv) WithBuilder(builder string) *BuildxEnv {
	b.builder = builder
	return b
}

func (b *BuildxEnv) bootstrap(ctx context.Context) (retErr error) {
	if b.client != nil {
		return nil
	}

	defer func() {
		if retErr != nil {
			return
		}

		b.supportedOnce.Do(func() {
			info, err := b.client.Info(ctx)
			if err != nil {
				b.supportedErr = err
				return
			}

			if !isSupported(info) {
				b.supportedErr = fmt.Errorf("buildkit version not supported: min version is v%s, got: %s", minVersion, info.BuildkitVersion.Version)
			}
		})
		if b.supportedErr != nil {
			b.client.Close()
			b.client = nil
			retErr = b.supportedErr
		}
	}()

	p, err := dockercfg.ConfigPath()
	if err != nil {
		return err
	}

	if out, err := exec.Command("docker", "buildx", "inspect", "--bootstrap", b.builder).CombinedOutput(); err != nil {
		return pkgerrors.Wrapf(err, "failed to bootstrap builder: %s", out)
	}

	configBase := filepath.Join(filepath.Dir(p), "buildx")

	if b.builder == "" {
		dt, err := os.ReadFile(filepath.Join(configBase, "current"))
		if err != nil {
			return pkgerrors.Wrap(err, "failed to read current builder")
		}

		type ref struct {
			Name string
			Key  string
		}
		var r ref
		if err := json.Unmarshal(dt, &r); err != nil {
			return err
		}

		if r.Name == "" {
			// This is the "default" buildx instance, aka dockerd's built-in buildkit.
			var tr transport.Doer
			if r.Key != "" {
				tr, err = transport.FromConnectionString(r.Key)
				if err != nil {
					return err
				}
			} else {
				tr, err = transport.DefaultTransport()
				if err != nil {
					return err
				}
			}

			b.client, err = client.New(ctx, "", buildkitopt.FromDocker(tr)...)
			return err
		}

		b.builder = r.Name
	}

	dt, err := os.ReadFile(filepath.Join(configBase, "instances", b.builder))
	if err != nil {
		return pkgerrors.Wrap(err, "failed to read buildx instance config")
	}

	var cfg buildxConfig
	if err := json.Unmarshal(dt, &cfg); err != nil {
		return pkgerrors.Wrap(err, "failed to unmarshal buildx config")
	}

	if cfg.Driver != "docker-container" {
		return pkgerrors.Errorf("unsupported buildx driver: %s", cfg.Driver)
	}

	if len(cfg.Nodes) == 0 {
		return pkgerrors.Errorf("no buildx nodes configured")
	}

	var errs []error
	for _, n := range cfg.Nodes {
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

		c, err := client.New(ctx, "", client.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return conn2, nil
		}))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Endpoint, err))
			continue
		}

		b.client = c
		b.ctr = ctr
		b.docker = dc
		return nil
	}

	// Could not create a buildkit client, return all errors.
	return errors.Join(errs...)
}

type buildxConfig struct {
	Driver string
	Nodes  []struct {
		Name     string
		Endpoint string
	}
}

func (b *BuildxEnv) Buildkit(ctx context.Context) (*client.Client, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err := b.bootstrap(ctx); err != nil {
		return nil, err
	}

	if b.client != nil {
		return b.client, nil
	}

	panic("unreachable: if you see this then this is a bug in the testenv bootstrap code")
}

type FrontendSpec struct {
	ID    string
	Build gwclient.BuildFunc
}

func (b *BuildxEnv) RunTest(ctx context.Context, t *testing.T, f gwclient.BuildFunc, frontends ...FrontendSpec) {
	c, err := b.Buildkit(ctx)
	if err != nil {
		t.Fatal(err)
	}

	ch, done := displaySolveStatus(ctx, t)

	var so client.SolveOpt
	withProjectRoot(t, &so)
	withGHCache(t, &so)

	_, err = c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		gwc = &clientForceDalecWithInput{gwc}

		b.mu.Lock()
		for _, f := range frontends {
			gwc = wrapWithInput(gwc, f.ID, f.Build)
		}
		b.mu.Unlock()
		return f(ctx, gwc)
	}, ch)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the display goroutine has finished.
	// Ensures there's no test output after the test has finished (which the test runner will complain about)
	<-done
}

// clientForceDalecWithInput is a gwclient.Client that forces the solve request to use the main dalec frontend.
type clientForceDalecWithInput struct {
	gwclient.Client
}

func (c *clientForceDalecWithInput) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	if req.Definition == nil {
		// Only inject the frontend when there is no "definition" set.
		// If a definition is set, it is intended for this to go directly to the buildkit solver.
		if err := withDalecInput(ctx, c.Client, &req); err != nil {
			return nil, err
		}
	}
	return c.Client.Solve(ctx, req)
}

// gwClientInputInject is a gwclient.Client that injects the result of a build func into the solve request as an input named by the id.
// This is used to inject a custom frontend into the solve request.
// This does not change what frontend is used, but it does add the custom frontend as an input to the solve request.
// This is so we don't need to have an actual external image from a registry or docker image store.
type gwClientInputInject struct {
	gwclient.Client

	id string
	f  gwclient.BuildFunc
}

func wrapWithInput(c gwclient.Client, id string, f gwclient.BuildFunc) *gwClientInputInject {
	return &gwClientInputInject{
		Client: c,
		id:     id,
		f:      f,
	}
}

func (c *gwClientInputInject) Solve(ctx context.Context, req gwclient.SolveRequest) (*gwclient.Result, error) {
	res, err := c.f(ctx, c.Client)
	if err != nil {
		return nil, err
	}
	if err := injectInput(ctx, res, c.id, &req); err != nil {
		return nil, err
	}
	return c.Client.Solve(ctx, req)
}
