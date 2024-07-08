package test

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend"
	"github.com/containerd/containerd/platforms"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/tonistiigi/fsutil/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"gotest.tools/v3/assert"
)

const (
	phonyRef       = "dalec/integration/frontend/phony"
	phonySignerRef = "dalec/integration/signer/phony"
)

func startTestSpan(ctx context.Context, t *testing.T) context.Context {
	ctx, span := otel.Tracer("").Start(ctx, t.Name())
	t.Cleanup(func() {
		if t.Failed() {
			span.SetStatus(codes.Error, "test failed")
		}
		span.End()
	})
	return ctx
}

// specToSolveRequest injects the spec as the build context into the solve request.
func specToSolveRequest(ctx context.Context, t *testing.T, spec *dalec.Spec, sr *gwclient.SolveRequest) {
	t.Helper()

	dt, err := yaml.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}

	def, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o644, dt)).Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if sr.FrontendInputs == nil {
		sr.FrontendInputs = make(map[string]*pb.Definition)
	}

	sr.FrontendInputs[dockerui.DefaultLocalNameContext] = def.ToPB()
	sr.FrontendInputs[dockerui.DefaultLocalNameDockerfile] = def.ToPB()
}

func readFile(ctx context.Context, t *testing.T, name string, res *gwclient.Result) []byte {
	t.Helper()

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: name,
	})
	if err != nil {
		stat, _ := ref.ReadDir(ctx, gwclient.ReadDirRequest{
			Path: filepath.Dir(name),
		})
		t.Fatalf("error reading file %q: %v, dir contents: \n%s", name, err, dirStatAsStringer(stat))
	}

	return dt
}

func maybeReadFile(ctx context.Context, name string, res *gwclient.Result) ([]byte, error) {
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	dt, err := ref.ReadFile(ctx, gwclient.ReadRequest{
		Filename: name,
	})
	if err != nil {
		stat, _ := ref.ReadDir(ctx, gwclient.ReadDirRequest{
			Path: filepath.Dir(name),
		})
		return nil, fmt.Errorf("error reading file %q: %v, dir contents: \n%s", name, err, dirStatAsStringer(stat))
	}

	return dt, nil
}

func statFile(ctx context.Context, t *testing.T, name string, res *gwclient.Result) {
	t.Helper()

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	_, err = ref.StatFile(ctx, gwclient.StatRequest{
		Path: name,
	})
	if err != nil {
		t.Fatalf("expected spec.yml to exist in debug/resolve target, got error: %v", err)
	}
}

func checkFile(ctx context.Context, t *testing.T, name string, res *gwclient.Result, expect []byte) {
	t.Helper()

	dt := readFile(ctx, t, name, res)
	if !bytes.Equal(dt, expect) {
		t.Fatalf("expected %q, got %q", string(expect), string(dt))
	}
}

func listTargets(ctx context.Context, t *testing.T, gwc gwclient.Client, spec *dalec.Spec) targets.List {
	t.Helper()

	sr := newSolveRequest(withListTargetsOnly, withSpec(ctx, t, spec))
	res := solveT(ctx, t, gwc, sr)

	dt, ok := res.Metadata["result.json"]
	if !ok {
		t.Fatal("missing result.json from list targets")
	}

	var ls targets.List
	if err := json.Unmarshal(dt, &ls); err != nil {
		t.Fatalf("could not unmsarshal list targets result: %v", err)
	}
	return ls
}

func containsTarget(ls targets.List, name string) bool {
	return slices.ContainsFunc(ls.Targets, func(tgt targets.Target) bool {
		return tgt.Name == name
	})
}

func checkTargetExists(t *testing.T, ls targets.List, name string) {
	t.Helper()

	if !containsTarget(ls, name) {
		names := make([]string, 0, len(ls.Targets))
		for _, tgt := range ls.Targets {
			names = append(names, tgt.Name)
		}

		t.Fatalf("did not find target %q:\n%v", name, names)
	}
}

type dirStatAsStringer []*types.Stat

func (d dirStatAsStringer) String() string {
	var buf bytes.Buffer
	buf.WriteString("\n")
	for _, s := range d {
		fmt.Fprintf(&buf, "%s %s %d %d\n", s.GetPath(), fs.FileMode(s.Mode), s.Uid, s.Gid)
	}
	return buf.String()
}

type newSolveRequestConfig struct {
	req              *gwclient.SolveRequest
	noFillSpecFields bool
}

// srOpt is used by [newSolveRequest] to apply changes to a [gwclient.SolveRequest].
type srOpt func(*newSolveRequestConfig)

func newSolveRequest(opts ...srOpt) gwclient.SolveRequest {
	cfg := newSolveRequestConfig{
		req: &gwclient.SolveRequest{Evaluate: true},
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return *cfg.req
}

func withPlatform(platform platforms.Platform) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		cfg.req.FrontendOpt["platform"] = platforms.Format(platform)
	}
}

func withBuildArg(k, v string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		cfg.req.FrontendOpt["build-arg:"+k] = v
	}
}

func withSpec(ctx context.Context, t *testing.T, spec *dalec.Spec) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if spec != nil && !cfg.noFillSpecFields {
			if spec.Packager == "" {
				spec.Packager = "test"
			}
			if spec.Website == "" {
				spec.Website = "https://github.com/Azure/dalec"
			}
		}
		specToSolveRequest(ctx, t, spec, cfg.req)
	}
}

func withSubrequest(id string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		cfg.req.FrontendOpt["requestid"] = id
	}
}

func withBuildTarget(target string) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		cfg.req.FrontendOpt["target"] = target
	}
}

// withListTargetsOnly sets up the request so that we do a subrequest to just list targets
// None of the targets will be run with this set.
func withListTargetsOnly(cfg *newSolveRequestConfig) {
	withSubrequest(targets.RequestTargets)(cfg)
}

func solveT(ctx context.Context, t *testing.T, gwc gwclient.Client, req gwclient.SolveRequest) *gwclient.Result {
	t.Helper()
	res, err := gwc.Solve(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func withMainContext(ctx context.Context, t *testing.T, st llb.State) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		if cfg.req.FrontendInputs == nil {
			cfg.req.FrontendInputs = make(map[string]*pb.Definition)
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}

		cfg.req.FrontendInputs[dockerui.DefaultLocalNameContext] = def.ToPB()
	}
}

func withBuildContext(ctx context.Context, t *testing.T, name string, st llb.State) srOpt {
	return func(cfg *newSolveRequestConfig) {
		if cfg.req.FrontendOpt == nil {
			cfg.req.FrontendOpt = make(map[string]string)
		}
		if cfg.req.FrontendInputs == nil {
			cfg.req.FrontendInputs = make(map[string]*pb.Definition)
		}

		def, err := st.Marshal(ctx)
		if err != nil {
			t.Fatal(err)
		}

		cfg.req.FrontendOpt["context:"+name] = "input:" + name
		cfg.req.FrontendInputs[name] = def.ToPB()
	}
}

func reqToState(ctx context.Context, gwc gwclient.Client, sr gwclient.SolveRequest, t *testing.T) llb.State {
	t.Helper()
	res := solveT(ctx, t, gwc, sr)

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	st, err := ref.ToState()
	if err != nil {
		t.Fatal(err)
	}

	dt, ok := res.Metadata[exptypes.ExporterPlatformsKey]
	if ok {
		var pls exptypes.Platforms
		if err := json.Unmarshal(dt, &pls); err != nil {
			t.Fatal(err)
		}
		st = st.Platform(pls.Platforms[0].Platform)
	}

	return st
}

func readDefaultPlatform(ctx context.Context, t *testing.T, gwc gwclient.Client) platforms.Platform {
	req := newSolveRequest(withSubrequest(frontend.KeyDefaultPlatform), withSpec(ctx, t, nil))
	res := solveT(ctx, t, gwc, req)

	dt := res.Metadata["result.json"]
	var p platforms.Platform

	err := json.Unmarshal(dt, &p)
	assert.NilError(t, err)
	return p
}

func elfToPlatform(f *elf.File, target *ocispecs.Platform) {
	switch f.Machine {
	case elf.EM_X86_64:
		target.Architecture = "amd64"
	case elf.EM_ARM:
		target.Architecture = "arm"
		// TODO: subarch?
	case elf.EM_AARCH64:
		target.Architecture = "arm64"
	case elf.EM_PPC64:
		if f.ByteOrder == binary.LittleEndian {
			target.Architecture = "ppc64le"
		}
		target.Architecture = "ppc64"
	case elf.EM_S390:
		target.Architecture = "s390x"
	case elf.EM_RISCV:
		target.Architecture = "riscv64"
	}
}

type platformsAsStringer []ocispecs.Platform

func (ls platformsAsStringer) String() string {
	collect := make([]string, 0, len(ls))
	for _, p := range ls {
		collect = append(collect, platforms.Format(p))
	}

	return strings.Join(collect, ", ")
}

func readResultPlatforms(t *testing.T, res *gwclient.Result) []ocispecs.Platform {
	dt, ok := res.Metadata[exptypes.ExporterPlatformsKey]
	if !ok {
		return nil
	}

	var pls exptypes.Platforms
	if err := json.Unmarshal(dt, &pls); err != nil {
		t.Fatal(err)
	}

	out := make([]ocispecs.Platform, 0, len(pls.Platforms))
	for _, p := range pls.Platforms {
		out = append(out, p.Platform)
	}

	return out
}

type platformStringer ocispecs.Platform

func (p platformStringer) String() string {
	return platforms.Format(ocispecs.Platform(p))
}
