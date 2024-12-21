package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path"
	"sync"
	"syscall"
	"time"

	"github.com/Azure/dalec/test/testenv"
	"github.com/docker/cli/cli/config"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

func main() {
	buildx := testenv.New()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		sigCtx, cancelSig := signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancelSig()
		defer cancel()

		select {
		case <-done:
		case <-sigCtx.Done():
			cancel()

			select {
			case <-time.After(30 * time.Second):
				fmt.Fprintln(os.Stderr, "timeout waiting for build to cancel")
				os.Exit(int(syscall.SIGINT))
			case <-done:
			}
		}

	}()

	client, err := buildx.Buildkit(ctx)
	if err != nil {
		panic(err)
	}

	if err := do(ctx, client); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func do(ctx context.Context, c *client.Client) (retErr error) {
	ch := make(chan *client.SolveStatus)
	so := client.SolveOpt{}

	err := withSolveOpts(
		&so,
		withRegistryAuth,
		testenv.WithProjectRoot,
	)
	if err != nil {
		return err
	}

	d, err := progressui.NewDisplay(os.Stderr, progressui.AutoMode)
	if err != nil {
		return nil
	}

	chErr := make(chan error, 1)
	go func() {
		warns, err := d.UpdateFrom(ctx, ch)

		for _, w := range warns {
			logrus.Warn(string(w.Short))
		}

		chErr <- err
	}()

	defer func() {
		err := <-chErr
		if retErr == nil {
			retErr = err
		}
	}()

	_, err = c.Build(ctx, so, "", build, ch)
	return err
}

func build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	targets := []string{
		"mariner2",
		"azlinux3",
		"bionic",
		"focal",
		"jammy",
		"noble",
		"bullseye",
		"bookworm",
		"windowscross",
	}

	eg, ctx := errgroup.WithContext(ctx)

	bctx, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o644, []byte("null"))).Marshal(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "creating build context")
	}
	for _, t := range targets {
		t := t
		eg.Go(func() error {
			f := buildWorker(t, bctx.ToPB())
			_, err := f(ctx, client)
			return errors.Wrap(err, t)
		})
	}

	return gwclient.NewResult(), eg.Wait()
}

func buildWorker(t string, bctx *pb.Definition) gwclient.BuildFunc {
	return func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
		req := gwclient.SolveRequest{
			FrontendOpt: map[string]string{
				"target": path.Join(t, "worker"),
			},
			FrontendInputs: map[string]*pb.Definition{
				dockerui.DefaultLocalNameContext:    bctx,
				dockerui.DefaultLocalNameDockerfile: bctx,
			},
			Evaluate: true,
		}

		if err := testenv.WithDalecInput(ctx, client, &req); err != nil {
			return nil, errors.Wrap(err, "error loading dalec frontend")
		}

		return client.Solve(ctx, req)
	}
}

func withRegistryAuth(so *client.SolveOpt) error {
	auth, err := registryAuthOnce()
	if err != nil {
		return err
	}
	so.Session = append(so.Session, auth)
	return nil
}

type solveOption func(so *client.SolveOpt) error

func withSolveOpts(so *client.SolveOpt, opts ...solveOption) error {
	for _, o := range opts {
		if err := o(so); err != nil {
			return err
		}
	}
	return nil
}

var registryAuthOnce = sync.OnceValues(getRegistryAuth)

func getRegistryAuth() (session.Attachable, error) {
	errBuf := bytes.NewBuffer(nil)
	auth := authprovider.NewDockerAuthProvider(config.LoadDefaultConfigFile(errBuf), nil)
	if errBuf.Len() > 0 {
		return nil, &bufErr{errBuf}
	}
	return auth, nil
}

type bufErr struct {
	fmt.Stringer
}

func (b *bufErr) Error() string {
	return b.String()
}
