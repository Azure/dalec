package test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/sync/errgroup"
)

var (
	localFrontendOnce   sync.Once
	localFrontendRef    gwclient.Reference
	localFrontendConfig []byte
)

func buildLocalFrontend(ctx context.Context, c gwclient.Client) (gwclient.Reference, []byte, error) {
	var err error
	localFrontendOnce.Do(func() {
		var dc *dockerui.Client
		dc, err = dockerui.NewClient(c)
		if err != nil {
			err = errors.Wrap(err, "error creating dockerui client")
			return
		}

		var buildCtx *llb.State
		buildCtx, err = dc.MainContext(ctx)
		if err != nil {
			err = errors.Wrap(err, "error getting main context")
			return
		}

		var def *llb.Definition
		def, err = buildCtx.Marshal(ctx)
		if err != nil {
			err = errors.Wrap(err, "error marshaling main context")
			return
		}

		// Can't use the state from `MainContext` because it filters out
		// whatever was in `.dockerignore`, which may include `Dockerfile`,
		// which we need.
		var dfDef *llb.Definition
		dfDef, err = llb.Local(dockerui.DefaultLocalNameDockerfile, llb.IncludePatterns([]string{"Dockerfile"})).Marshal(ctx)
		if err != nil {
			err = errors.Wrap(err, "error marshaling Dockerfile context")
			return
		}

		defPB := def.ToPB()
		var res *gwclient.Result
		res, err = c.Solve(ctx, gwclient.SolveRequest{
			Frontend:    "dockerfile.v0",
			FrontendOpt: map[string]string{},
			FrontendInputs: map[string]*pb.Definition{
				dockerui.DefaultLocalNameContext:    defPB,
				dockerui.DefaultLocalNameDockerfile: dfDef.ToPB(),
			},
		})
		if err != nil {
			err = errors.Wrap(err, "solve")
			return
		}
		localFrontendRef, err = res.SingleRef()
		if err != nil {
			err = errors.Wrap(err, "single ref")
			return
		}

		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		if !ok {
			err = errors.New("missing image config")
			return
		}
		localFrontendConfig = dt
	})
	return localFrontendRef, localFrontendConfig, err
}

// withProjectRoot adds the current project root as the build context for the solve request.
func withProjectRoot(opts *client.SolveOpt) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	projectRoot, err := lookupProjectRoot(cwd)
	if err != nil {
		return err
	}

	if opts.LocalDirs == nil {
		opts.LocalDirs = make(map[string]string)
	}
	opts.LocalDirs[dockerui.DefaultLocalNameContext] = projectRoot
	opts.LocalDirs[dockerui.DefaultLocalNameDockerfile] = projectRoot

	return nil
}

// lookupProjectRoot looks up the project root from the current working directory.
// This is needed so the test suite can be run from any directory within the project.
func lookupProjectRoot(cur string) (string, error) {
	if _, err := os.Stat(filepath.Join(cur, "go.mod")); err != nil {
		if cur == "/" || cur == "." {
			return "", errors.Wrap(err, "could not find project root")
		}
		if os.IsNotExist(err) {
			return lookupProjectRoot(filepath.Dir(cur))
		}
		return "", err
	}

	return cur, nil
}

// withLocalFrontendInputs adds the neccessary options to a solve request to use
// the locally built frontend as an input to the solve request.
// This only works with buildkit >= 0.12
func withLocaFrontendInputs(ctx context.Context, gwc gwclient.Client, opts *gwclient.SolveRequest, fID string) (retErr error) {
	ctx, span := otel.Tracer("").Start(ctx, "build local froontend")
	defer func() {
		if retErr != nil {
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	fRef, fCfg, err := buildLocalFrontend(ctx, gwc)
	if err != nil {
		return errors.Wrap(err, "error building local frontend")
	}

	fSt, err := fRef.ToState()
	if err != nil {
		return errors.Wrap(err, "error getting local frontend state")
	}

	fSt, err = fSt.WithImageConfig(fCfg)
	if err != nil {
		return errors.Wrap(err, "error setting local frontend image config")
	}

	fDef, err := fSt.Marshal(ctx)
	if err != nil {
		return errors.Wrap(err, "error marshaling local frontend state")
	}

	if opts.FrontendOpt == nil {
		opts.FrontendOpt = make(map[string]string)
	}

	opts.FrontendOpt["source"] = fID
	opts.FrontendOpt["context:"+fID] = "input:" + fID
	opts.Frontend = "gateway.v0"

	if opts.FrontendInputs == nil {
		opts.FrontendInputs = make(map[string]*pb.Definition)
	}
	opts.FrontendInputs[fID] = fDef.ToPB()

	meta := map[string][]byte{
		exptypes.ExporterImageConfigKey: fCfg,
	}
	metaDt, err := json.Marshal(meta)
	if err != nil {
		return errors.Wrap(err, "error marshaling local frontend metadata")
	}
	opts.FrontendOpt["input-metadata:"+fID] = string(metaDt)

	return nil
}

func displaySolveStatus(ctx context.Context, group *errgroup.Group) chan *client.SolveStatus {
	ch := make(chan *client.SolveStatus)
	group.Go(func() error {
		_, _ = progressui.DisplaySolveStatus(ctx, nil, os.Stderr, ch)
		return nil
	})
	return ch
}
