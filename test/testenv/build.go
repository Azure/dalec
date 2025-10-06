package testenv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var StreamLiveOutput bool

func buildBaseFrontend(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
	dc, err := dockerui.NewClient(c)
	if err != nil {
		return nil, errors.Wrap(err, "error creating dockerui client")
	}

	buildCtx, err := dc.MainContext(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error getting main context")
	}

	def, err := buildCtx.Marshal(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling main context")
	}

	// Can't use the state from `MainContext` because it filters out
	// whatever was in `.dockerignore`, which may include `Dockerfile`,
	// which we need.
	dockerfileDef, err := llb.Local(dockerui.DefaultLocalNameDockerfile, llb.IncludePatterns([]string{"Dockerfile"})).Marshal(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling Dockerfile context")
	}

	defPB := def.ToPB()
	return c.Solve(ctx, gwclient.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: map[string]string{},
		FrontendInputs: map[string]*pb.Definition{
			dockerui.DefaultLocalNameContext:    defPB,
			dockerui.DefaultLocalNameDockerfile: dockerfileDef.ToPB(),
		},
		Evaluate: true,
	})
}

// InjectInput adds the necessary options to a solve request to use the output of the provided build function as an input to the solve request.
func injectInput(ctx context.Context, res *gwclient.Result, id string, req *gwclient.SolveRequest) (retErr error) {
	ctx, span := otel.Tracer("").Start(ctx, "build input "+id)
	defer func() {
		if retErr != nil {
			span.SetStatus(codes.Error, retErr.Error())
		}
		span.End()
	}()

	ref, err := res.SingleRef()
	if err != nil {
		return err
	}

	st, err := ref.ToState()
	if err != nil {
		return err
	}

	dt := res.Metadata[exptypes.ExporterImageConfigKey]

	if dt != nil {
		st, err = st.WithImageConfig(dt)
		if err != nil {
			return err
		}
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		return err
	}

	if req.FrontendOpt == nil {
		req.FrontendOpt = make(map[string]string)
	}
	req.FrontendOpt["context:"+id] = "input:" + id
	if req.FrontendInputs == nil {
		req.FrontendInputs = make(map[string]*pb.Definition)
	}
	req.FrontendInputs[id] = def.ToPB()
	if dt != nil {
		meta := map[string][]byte{
			exptypes.ExporterImageConfigKey: dt,
		}
		metaDt, err := json.Marshal(meta)
		if err != nil {
			return errors.Wrap(err, "error marshaling local frontend metadata")
		}
		req.FrontendOpt["input-metadata:"+id] = string(metaDt)
	}

	return nil
}

// withDalecInput adds the necessary options to a solve request to use
// the locally built frontend as an input to the solve request.
// This only works with buildkit >= 0.12
func withDalecInput(ctx context.Context, gwc gwclient.Client, opts *gwclient.SolveRequest) error {
	id := identity.NewID()
	res, err := buildBaseFrontend(ctx, gwc)
	if err != nil {
		return errors.Wrap(err, "error building local frontend")
	}
	if err := injectInput(ctx, res, id, opts); err != nil {
		return errors.Wrap(err, "error adding local frontend as input")
	}

	opts.FrontendOpt["source"] = id
	opts.Frontend = "gateway.v0"
	return nil
}

type testWriter struct {
	t        *testing.T
	buffered *bytes.Buffer
}

func (tw *testWriter) Write(p []byte) (n int, err error) {
	tw.buffered.Write(p)

	scanner := bufio.NewScanner(tw.buffered)
	var reset bool
	for scanner.Scan() {
		if scanner.Text() == "" {
			continue
		}
		tw.t.Log(scanner.Text())
		reset = true
	}

	if reset {
		tw.buffered.Reset()
	}
	if err := scanner.Err(); err != nil {
		return -1, err
	}

	return len(p), nil
}

func (tw *testWriter) Flush() {
	if tw.buffered.Len() == 0 {
		return
	}

	tw.t.Log(tw.buffered.String())
	tw.buffered.Reset()
}

func getBuildOutputWriter(ctx context.Context, t *testing.T, done <-chan struct{}) io.Writer {
	if StreamLiveOutput {
		w := &testWriter{t: t, buffered: &bytes.Buffer{}}
		t.Cleanup(w.Flush)
		return w
	}

	dir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "build.log"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("error opening temp file: %v", err)
	}

	t.Cleanup(func() {
		defer f.Close()

		select {
		case <-ctx.Done():
			return
		case <-done:
		}

		_, err = f.Seek(0, io.SeekStart)
		if err != nil {
			t.Log(err)
			return
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if scanner.Text() == "" {
				continue
			}
			t.Log(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			t.Log(err)
		}

	})
	return f
}

func displaySolveStatus(ctx context.Context, t *testing.T) chan *client.SolveStatus {
	ch := make(chan *client.SolveStatus)
	done := make(chan struct{})

	w := getBuildOutputWriter(ctx, t, done)
	display, err := progressui.NewDisplay(w, progressui.AutoMode, progressui.WithPhase(t.Name()))
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		defer close(done)

		_, err := display.UpdateFrom(ctx, ch)
		if err != nil {
			t.Log(err)
		}
	}()

	return ch
}

// withProjectRoot adds the current project root as the build context for the solve request.
func withProjectRoot(t *testing.T, opts *client.SolveOpt) {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	projectRoot, err := lookupProjectRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}

	if opts.LocalDirs == nil {
		opts.LocalDirs = make(map[string]string)
	}
	opts.LocalDirs[dockerui.DefaultLocalNameContext] = projectRoot
	opts.LocalDirs[dockerui.DefaultLocalNameDockerfile] = projectRoot
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
