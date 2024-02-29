package frontend

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/dalec"
	"github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var HandlerNotFound = errors.New("handler not found")

// `projectWrapper` provides some additional functionality to the
// project struct while leaving the Project struct as simple data.
type projectWrapper struct {
	*dalec.Project
	target       string
	isSingleSpec bool
}

func (p *projectWrapper) GetProject() *dalec.Project {
	return p.Project
}

// `GetSpec` returns the main spec to build. When the Project is a
// single spec, return that spec. When the Project is a slice of
// specs, return the first matching spec with the `name` as specified
// when `NewProjectWrapper` was called. If no name was specified,
// return the last.
func (p *projectWrapper) GetSpec() *dalec.Spec {
	if p.isSingleSpec {
		return p.Spec
	}

	for _, spec := range p.Specs {
		if spec.Name == p.target {
			return &spec
		}
	}

	// The `loadProject` function has already ensured there is at
	// least one spec.
	return &p.Specs[len(p.Specs)-1]
}

// This is a placeholder until it is implemented by PR #146
func (p *projectWrapper) GetGraph() *dalec.Graph {
	panic("unimplemented")
}

type projectConfig struct {
	target string
}

type projectOpt func(*projectConfig) error

func withTarget(name string) projectOpt {
	return func(cfg *projectConfig) error {
		cfg.target = name
		return nil
	}
}

func newProjectWrapper(p *dalec.Project, opts ...projectOpt) (*projectWrapper, error) {
	config := &projectConfig{}

	for _, o := range opts {
		if err := o(config); err != nil {
			return nil, fmt.Errorf("failed to set up project wrapper config: %w", err)
		}
	}

	pw := projectWrapper{
		Project:      p,
		target:       config.target,
		isSingleSpec: false,
	}

	// Specs cannot be directly compared because they contain
	// slices and maps.
	if pw.Spec != nil {
		var e dalec.Spec
		j, _ := json.Marshal(&e)
		k, _ := json.Marshal(pw.Spec)
		pw.isSingleSpec = slices.Compare(j, k) != 0
	}

	if pw.isSingleSpec && pw.target != "" {
		return nil, fmt.Errorf("name %q requested as project target, but project is a single spec", pw.target)
	}

	return &pw, nil

}

func loadProject(ctx context.Context, client *dockerui.Client, target string) (*projectWrapper, error) {
	src, err := client.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("could not read spec file: %w", err)
	}

	project, err := dalec.LoadProject(bytes.TrimSpace(src.Data))
	if err != nil {
		return nil, err
	}

	pw, err := newProjectWrapper(project, withTarget(target))
	if err != nil {
		return nil, fmt.Errorf("error initializing project: %w", err)
	}

	if !pw.isSingleSpec && len(project.Specs) == 0 {
		return nil, fmt.Errorf("no specs provided")
	}

	if pw.isSingleSpec && len(project.Specs) != 0 {
		return nil, fmt.Errorf("format of project must be either a single spec or a list of specs nested under the `specs` key")
	}

	validateAndFillDefaults := func(s *dalec.Spec) error {
		if err := s.Validate(); err != nil {
			return err
		}

		s.FillDefaults()
		return nil
	}

	switch {
	case pw.isSingleSpec:
		if err := validateAndFillDefaults(project.Spec); err != nil {
			return nil, fmt.Errorf("error loading project: %w", err)
		}
	case len(project.Specs) != 0:
		for i := range project.Specs {
			if err := validateAndFillDefaults(&project.Specs[i]); err != nil {
				return nil, fmt.Errorf("error validating project spec with name %q: %w", project.Specs[i].Name, err)
			}
		}
	}

	return pw, nil
}

func listBuildTargets(group string) []*targetWrapper {
	if group != "" {
		return registeredHandlers.GetGroup(group)
	}
	return registeredHandlers.All()
}

func lookupHandler(target string) (BuildFunc, error) {
	if target == "" {
		return registeredHandlers.Default().Build, nil
	}

	t := registeredHandlers.Get(target)
	if t == nil {
		return nil, HandlerNotFound
	}
	return t.Build, nil
}

func makeRequestHandler(target string) dockerui.RequestHandler {
	h := dockerui.RequestHandler{AllowOther: true}

	h.ListTargets = func(ctx context.Context) (*targets.List, error) {
		var ls targets.List
		for _, tw := range listBuildTargets(target) {
			ls.Targets = append(ls.Targets, tw.Target)
		}
		return &ls, nil
	}

	return h
}

func getOS(platform ocispecs.Platform) string {
	return platform.OS
}

func getArch(platform ocispecs.Platform) string {
	return platform.Architecture
}

func getVariant(platform ocispecs.Platform) string {
	return platform.Variant
}

func getPlatformFormat(platform ocispecs.Platform) string {
	return platforms.Format(platform)
}

var passthroughGetters = map[string]func(ocispecs.Platform) string{
	"OS":       getOS,
	"ARCH":     getArch,
	"VARIANT":  getVariant,
	"PLATFORM": getPlatformFormat,
}

func fillPlatformArgs(prefix string, args map[string]string, platform ocispecs.Platform) {
	for attr, getter := range passthroughGetters {
		args[prefix+attr] = getter(platform)
	}
}

// Build is the main entrypoint for the dalec frontend.
func Build(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	if !SupportsDiffMerge(client) {
		dalec.DisableDiffMerge(true)
	}

	bc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, fmt.Errorf("could not create build client: %w", err)
	}

	dalecTarget := bc.Target
	specTarget := ""

	handlerFunc, err := lookupHandler(bc.Target)
	if errors.Is(err, HandlerNotFound) {
		tgt, rest, ok := strings.Cut(bc.Target, "/")
		if !ok {
			return nil, fmt.Errorf("unable to parse target %q", bc.Target)
		}

		specTarget = tgt
		dalecTarget = rest

		handlerFunc, err = lookupHandler(dalecTarget)
		if err != nil {
			return nil, fmt.Errorf("can't route target %q: %w", bc.Target, err)
		}
	}

	project, err := loadProject(ctx, bc, specTarget)
	if err != nil {
		return nil, fmt.Errorf("error loading spec: %w", err)
	}

	if err := registerSpecHandlers(ctx, project, client); err != nil {
		return nil, err
	}

	res, handled, err := bc.HandleSubrequest(ctx, makeRequestHandler(dalecTarget))
	if err != nil || handled {
		return res, err
	}

	if !handled {
		// Handle additional subrequests supported by dalec
		requestID := client.BuildOpts().Opts[requestIDKey]
		switch requestID {
		case dalecSubrequstForwardBuild:
		case "":
		default:
			return nil, fmt.Errorf("unknown request id %q", requestID)
		}
	}

	rb, err := bc.Build(ctx, func(ctx context.Context, platform *ocispecs.Platform, idx int) (gwclient.Reference, *image.Image, error) {
		var targetPlatform, buildPlatform ocispecs.Platform
		if platform != nil {
			targetPlatform = *platform
		} else {
			targetPlatform = platforms.DefaultSpec()
		}

		// the dockerui client, given the current implementation, should only ever have
		// a single build platform
		if len(bc.BuildPlatforms) != 1 {
			return nil, nil, fmt.Errorf("expected exactly one build platform, got %d", len(bc.BuildPlatforms))
		}
		buildPlatform = bc.BuildPlatforms[0]

		args := dalec.DuplicateMap(bc.BuildArgs)
		fillPlatformArgs("TARGET", args, targetPlatform)
		fillPlatformArgs("BUILD", args, buildPlatform)

		spec := project.GetSpec()
		if err := spec.SubstituteArgs(args); err != nil {
			return nil, nil, err
		}

		return handlerFunc(ctx, client, spec)
	})
	if err != nil {
		return nil, err
	}

	return rb.Finalize()
}
