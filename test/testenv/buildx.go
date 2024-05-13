package testenv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cpuguy83/dockercfg"
	"github.com/cpuguy83/go-docker"
	"github.com/cpuguy83/go-docker/buildkitopt"
	"github.com/cpuguy83/go-docker/container"
	"github.com/cpuguy83/go-docker/transport"
	"github.com/moby/buildkit/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	pkgerrors "github.com/pkg/errors"
)

type BuildxEnv struct {
	builder string

	mu     sync.Mutex
	client *client.Client

	ctr    *container.Container
	docker *docker.Client

	supportedOnce sync.Once
	supportedErr  error

	refs map[string]gwclient.BuildFunc
}

func New() *BuildxEnv {
	return &BuildxEnv{}
}

func (b *BuildxEnv) WithBuilder(builder string) *BuildxEnv {
	b.builder = builder
	return b
}

// Load loads the output of the speecified [gwclient.BuildFunc] into the buildkit instance.
func (b *BuildxEnv) Load(ctx context.Context, id string, f gwclient.BuildFunc) error {
	if b.refs == nil {
		b.refs = make(map[string]gwclient.BuildFunc)
	}
	b.refs[id] = f
	return nil
}

func (b *BuildxEnv) version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "buildx", "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", pkgerrors.Wrap(err, string(out))
	}

	fields := strings.Fields(string(out))

	if len(fields) != 3 {
		return "", errors.New("could not determine buildx version")
	}

	ver, _, _ := strings.Cut(strings.TrimPrefix(fields[1], "v"), "-")
	if strings.Count(ver, ".") < 2 {
		return "", fmt.Errorf("unexpected version format: %s", ver)
	}
	return ver, nil
}

func (b *BuildxEnv) supportsDialStdio(ctx context.Context) (bool, error) {
	ver, err := b.version(ctx)
	if err != nil {
		return false, err
	}

	majorStr, other, _ := strings.Cut(ver, ".")
	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return false, pkgerrors.Wrapf(err, "could not parse major version number: %s", ver)
	}
	if major > 0 {
		return true, nil
	}

	minorStr, _, _ := strings.Cut(other, ".")
	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return false, pkgerrors.Wrapf(err, "could not parse major version number: %s", ver)
	}
	return minor >= 13, nil
}

var errDialStdioNotSupportedErr = errors.New("buildx dial-stdio not supported")

func (b *BuildxEnv) dialStdio(ctx context.Context) (bool, error) {
	ok, err := b.supportsDialStdio(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errDialStdioNotSupportedErr, err)
	}

	if !ok {
		return false, nil
	}

	c, err := client.New(ctx, "", client.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		args := []string{"buildx", "dial-stdio", "--progress=plain"}
		if b.builder != "" {
			args = append(args, "--builder="+b.builder)
		}

		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Env = os.Environ()

		c1, c2 := net.Pipe()
		cmd.Stdin = c1
		cmd.Stdout = c1

		// Use a pipe to check when the connection is actually complete
		// Also write all of stderr to an error buffer so we can have more details
		// in the error message when the command fails.
		r, w := io.Pipe()
		errBuf := bytes.NewBuffer(nil)
		ww := io.MultiWriter(w, errBuf)
		cmd.Stderr = ww

		if err := cmd.Start(); err != nil {
			return nil, err
		}

		go func() {
			err := cmd.Wait()
			c1.Close()
			// pkgerrors.Wrap will return nil if err is nil, otherwise it will give
			// us a wrapped error with the buffered stderr fromt he command.
			w.CloseWithError(pkgerrors.Wrapf(err, "%s", errBuf))
		}()

		defer r.Close()

		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			txt := strings.ToLower(scanner.Text())

			if strings.HasPrefix(txt, "#1 dialing builder") && strings.HasSuffix(txt, "done") {
				go func() {
					// Continue draining stderr so the process does not get blocked
					_, _ = io.Copy(io.Discard, r)
				}()
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}

		return c2, nil
	}))

	if err != nil {
		return false, err
	}

	b.client = c
	return true, nil
}

// bootstrap is ultimately responsible for creating a buildkit client.
// It looks like the buildx config on the client (typically in $HOME/.docker/buildx) to determine how to connect to the configured buildkit.
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
				b.supportedErr = pkgerrors.WithStack(err)
				return
			}

			if !supportsFrontendAsInput(info) {
				b.supportedErr = fmt.Errorf("buildkit version not supported: min version is v%s, got: %s", minVersion, info.BuildkitVersion.Version)
			}
		})
		if b.supportedErr != nil {
			b.client.Close()
			b.client = nil
			retErr = b.supportedErr
		}
	}()

	ok, err := b.dialStdio(ctx)
	if err != nil && !errors.Is(err, errDialStdioNotSupportedErr) {
		return err
	}

	if ok {
		return nil
	}

	// Fallback for older versions of buildx
	p, err := dockercfg.ConfigPath()
	if err != nil {
		return pkgerrors.WithStack(err)
	}

	if out, err := exec.Command("docker", "buildx", "inspect", "--bootstrap", b.builder).CombinedOutput(); err != nil {
		return pkgerrors.Wrapf(err, "failed to bootstrap builder: %s", out)
	}

	configBase := filepath.Join(filepath.Dir(p), "buildx")

	// builder is empty, so we need to check what the currently configured buildx builder is.
	// This is stored int he buildx config in (typically) $HOME/.docker/buildx (the `dockercfg` lib determines where this actually is).
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
					return pkgerrors.Wrap(err, r.Key)
				}
			} else {
				tr, err = transport.DefaultTransport()
				if err != nil {
					return pkgerrors.WithStack(err)
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

	// On a typical client this would be a single node, but there could be multiple registered with he same builder name.
	// We'll just try them all until we find one that works.
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

// withResolveLocal tells buildkit to prefer local images when resolving image references.
// This prevents uneccessary API requests to registries.
func withResolveLocal(so *client.SolveOpt) {
	if so.FrontendAttrs == nil {
		so.FrontendAttrs = make(map[string]string)
	}

	if _, ok := so.FrontendAttrs[pb.AttrImageResolveMode]; ok {
		// Don't set it if it's already set.
		return
	}

	so.FrontendAttrs[pb.AttrImageResolveMode] = pb.AttrImageResolveModePreferLocal
}

func (b *BuildxEnv) RunTest(ctx context.Context, t *testing.T, f gwclient.BuildFunc) {
	c, err := b.Buildkit(ctx)
	if err != nil {
		t.Fatalf("%+v", err)
	}

	ch, done := displaySolveStatus(ctx, t)

	var so client.SolveOpt
	withProjectRoot(t, &so)
	withGHCache(t, &so)
	withResolveLocal(&so)

	_, err = c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		gwc = &clientForceDalecWithInput{gwc}

		b.mu.Lock()
		for id, f := range b.refs {
			gwc = wrapWithInput(gwc, id, f)
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
