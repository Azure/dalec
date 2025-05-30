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
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/dalec/sessionutil/socketprovider"
	"github.com/moby/buildkit/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	pkgerrors "github.com/pkg/errors"
	"google.golang.org/protobuf/proto"
	"gotest.tools/v3/assert"
)

type BuildxEnv struct {
	builder string

	mu     sync.Mutex
	client *client.Client

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

// Load loads the output of the specified [gwclient.BuildFunc] into the buildkit instance.
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

var errDialStdioNotSupported = errors.New("buildx dial-stdio not supported")

func (b *BuildxEnv) dialStdio(ctx context.Context) error {
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
			// us a wrapped error with the buffered stderr from he command.
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
		return err
	}

	b.client = c
	return nil
}

// bootstrap is ultimately responsible for creating a buildkit client.
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
			b.client.Close() //nolint:errcheck
			b.client = nil
			retErr = b.supportedErr
		}
	}()

	ok, err := b.supportsDialStdio(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", errDialStdioNotSupported, err)
	}

	if !ok {
		return errDialStdioNotSupported
	}

	return b.dialStdio(ctx)
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
// This prevents unnecessary API requests to registries.
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

type TestFunc func(context.Context, gwclient.Client)

type TestRunnerConfig struct {
	// SolveStatusFn replaces the builtin status logger with a custom implementation.
	// This is useful particularly if you need to inspect the solve statuses.
	SolveStatusFn func(*client.SolveStatus)
	// SocketProxies is the list of sockets that need to be forwarded into the build.
	SocketProxies []socketprovider.ProxyConfig
}

type TestRunnerOpt func(*TestRunnerConfig)

// SolveStatus is convenience wrapper for [client.SolveStatus] to help disambiguate
// imports of the [client] package.
type SolveStatus = client.SolveStatus

func WithSolveStatusFn(f func(*SolveStatus)) TestRunnerOpt {
	return func(cfg *TestRunnerConfig) {
		cfg.SolveStatusFn = f
	}
}

func WithSocketProxies(proxies ...socketprovider.ProxyConfig) TestRunnerOpt {
	return func(cfg *TestRunnerConfig) {
		cfg.SocketProxies = append(cfg.SocketProxies, proxies...)
	}
}

func (b *BuildxEnv) RunTest(ctx context.Context, t *testing.T, f TestFunc, opts ...TestRunnerOpt) {
	var cfg TestRunnerConfig

	for _, o := range opts {
		o(&cfg)
	}

	c, err := b.Buildkit(ctx)
	if err != nil {
		t.Fatalf("%+v", err)
	}

	var (
		ch   chan *client.SolveStatus
		done <-chan struct{}
	)

	if cfg.SolveStatusFn != nil {
		chDone := make(chan struct{})

		ch = make(chan *client.SolveStatus, 1)
		done = chDone
		go func() {
			defer close(chDone)

			for msg := range ch {
				cfg.SolveStatusFn(msg)
			}
		}()
	} else {
		ch, done = displaySolveStatus(ctx, t)
	}

	var so client.SolveOpt
	withProjectRoot(t, &so)
	withResolveLocal(&so)
	withSocketProxies(t, cfg.SocketProxies)(&so)

	err = withSourcePolicy(&so)
	assert.NilError(t, err)

	_, err = c.Build(ctx, so, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		gwc = &clientForceDalecWithInput{gwc}

		b.mu.Lock()
		for id, f := range b.refs {
			gwc = wrapWithInput(gwc, id, f)
		}
		b.mu.Unlock()
		f(ctx, gwc)
		return gwclient.NewResult(), nil
	}, ch)

	// Make sure the display goroutine has finished.
	// Ensures there's no test output after the test has finished (which the test runner will complain about)
	<-done

	if err != nil {
		t.Fatal(err)
	}
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

func withSourcePolicy(so *client.SolveOpt) error {
	p := os.Getenv("EXPERIMENTAL_BUILDKIT_SOURCE_POLICY")
	if p == "" {
		return nil
	}

	dt, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("could not read source policy file: %w", err)
	}

	var pol spb.Policy
	if err := json.Unmarshal(dt, &pol); err != nil {
		// maybe it's in protobuf format?
		e2 := proto.Unmarshal(dt, &pol)
		if e2 != nil {
			return pkgerrors.Wrap(err, "failed to parse source policy")
		}
	}

	so.SourcePolicy = &pol
	return nil
}

func withSocketProxies(t *testing.T, proxies []socketprovider.ProxyConfig) func(*client.SolveOpt) {
	return func(so *client.SolveOpt) {
		t.Helper()
		if len(proxies) == 0 {
			return
		}

		handler, err := socketprovider.NewProxyHandler(proxies)
		assert.NilError(t, err)
		so.Session = append(so.Session, handler)
	}
}
