package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"

	daleccmd "github.com/Azure/dalec/cmd"
	"github.com/Azure/dalec/cmd/dalec-redirectio/redirectio"
	"github.com/Azure/dalec/frontend"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/pkg/errors"
	"github.com/vito/progrock"
)

func main() {
	if filepath.Base(os.Args[0]) == "dalec-redirectio" {
		redirectio.Main(os.Args[1:])
		return
	}

	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt)

	flag.Parse()

	switch flag.Arg(0) {
	case "build":
		if err := cmdBuild(ctx, flag.Args()[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "%+v\n", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", flag.Arg(0))
	}
}

type buildArgsFlagValue map[string]string

func (m buildArgsFlagValue) String() string {
	b := &strings.Builder{}
	for k, v := range m {
		fmt.Fprintf(b, "%s=%s ", strings.TrimPrefix("build-arg:", k), v)
	}
	return b.String()
}

func (m buildArgsFlagValue) Set(value string) error {
	parts := strings.SplitN("build-arg:"+value, "=", 2)
	if len(parts) != 2 {
		return errors.Errorf("expected key=value, got %q", value)
	}
	m[parts[0]] = parts[1]
	return nil
}

type outputValue struct {
	outputs []client.ExportEntry
}

func (o *outputValue) String() string {
	var b strings.Builder

	for _, e := range o.outputs {
		if e.OutputDir != "" {
			b.WriteString(e.OutputDir)
		}
	}
	return b.String()
}

func (ov *outputValue) Set(value string) error {
	if !strings.Contains(value, "=") {
		ov.outputs = append(ov.outputs, client.ExportEntry{Type: "local", OutputDir: value})
		return nil
	}

	opts := make(map[string]string)
	for _, kv := range strings.Split(value, ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return errors.Errorf("expected key=value, got %q", kv)
		}
		opts[k] = v
	}

	var e client.ExportEntry
	e.Type = opts["type"]
	if e.Type != "local" {
		return errors.Errorf("output type %q is not currently supported: only the local output type is supported", e.Type)
	}

	e.OutputDir = opts["dest"]
	if e.OutputDir == "" {
		return errors.Errorf("output destination is required")
	}

	ov.outputs = append(ov.outputs, e)
	return nil
}

func readIgnorefile() ([]string, error) {
	f, err := os.Open(".dockerignore")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	return ignorefile.ReadAll(f)
}

func cmdBuild(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("build", flag.ExitOnError)

	targetFl := flags.String("target", "", "target to build")
	specFileFl := flags.String("f", "", "path to spec file [required]")
	contextPathFl := flags.String("context", "", "path to context")

	buildArgs := make(buildArgsFlagValue)
	flags.Var(buildArgs, "build-arg", "build-args to set")

	outputs := &outputValue{}
	flags.Var(outputs, "output", "Output destination (format: \"type=local,dest=path\")")
	flags.Var(outputs, "o", "Output destination (format: \"type=local,dest=path\")")

	if err := flags.Parse(args); err != nil {
		return err
	}

	specFile := *specFileFl
	if specFile == "" {
		flags.SetOutput(os.Stderr)
		flags.Usage()
		return errors.New("spec file is required")
	}

	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, "docker", "buildx", "dial-stdio")
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	connErrBuf := bytes.NewBuffer(nil)
	c, err := client.New(ctx, "", client.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		c1, c2 := net.Pipe()

		cmd.Stdin = c1
		cmd.Stdout = c1
		cmd.Stderr = connErrBuf

		return c2, cmd.Start()
	}))

	if err != nil {
		return errors.Wrap(err, connErrBuf.String())
	}

	defer c.Close()

	ch := make(chan *client.SolveStatus)

	t := progrock.NewTape()
	r := progrock.NewRecorder(t)

	defer t.Close()
	defer r.Close()

	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	go func() {
		handleEvents(ctx, ch, r)
		cancel()
	}()

	f := func(ctx context.Context, u progrock.UIClient) error {
		excludes, err := readIgnorefile()
		if err != nil {
			return err
		}
		var bctxOpts []llb.LocalOption
		if len(excludes) > 0 {
			bctxOpts = append(bctxOpts, llb.ExcludePatterns(excludes))
		}

		bctxPtr := dockerui.DefaultMainContext(bctxOpts...)
		bctx := *bctxPtr

		solveOpt := client.SolveOpt{
			FrontendInputs: map[string]llb.State{
				"context":                bctx,
				"dockerfile":             llb.Local("dockerfile", llb.IncludePatterns([]string{filepath.Base(specFile)}), llb.WithCustomName("[internal] load spec file")),
				"dalec-current-frontend": llb.Local("dalec-current-frontend", llb.IncludePatterns([]string{filepath.Base(os.Args[0])}), llb.WithCustomName("[internal] load frontend binaries")),
			},
			FrontendAttrs: map[string]string{
				"target":   *targetFl,
				"filename": filepath.Base(specFile),
			},
			LocalDirs: map[string]string{
				"context":                *contextPathFl,
				"dockerfile":             filepath.Dir(specFile),
				"dalec-current-frontend": filepath.Dir(os.Args[0]),
			},
			Exports: outputs.outputs,
		}

		maps.Copy(solveOpt.FrontendAttrs, buildArgs)
		_, err = c.Build(ctx, solveOpt, "", func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
			daleccmd.LoadFrontend()
			return frontend.Build(ctx, &frontendClient{gwc})
		}, ch)

		return err
	}

	return progrock.DefaultUI().Run(ctx, t, f)
}

type frontendClient struct {
	gwclient.Client
}

func (f *frontendClient) CurrentFrontend() (*llb.State, error) {
	return daleccmd.CurrentFrontend()
}
