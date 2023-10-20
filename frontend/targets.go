package frontend

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/Azure/dalec"
	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/image"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/moby/buildkit/solver/pb"
	"github.com/pkg/errors"
)

const dalecTargetOptKey = "dalec.target"

type BuildFunc func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error)

type targetWrapper struct {
	bktargets.Target
	Build BuildFunc
}

type targetList struct {
	mu            sync.Mutex
	ls            map[string]*targetWrapper
	groupIdx      map[string][]*targetWrapper
	defaultTarget *targetWrapper
	lastTarget    *targetWrapper
	builtins      map[string]*targetWrapper
}

func (s *targetList) Add(group string, value *targetWrapper) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !strings.HasPrefix(value.Name+"/", group) {
		value.Name = path.Join(group, value.Name)
	}

	if _, ok := s.builtins[value.Name]; ok {
		panic("builtin target already exists: " + value.Name)
	}

	s.ls[value.Name] = value
	s.groupIdx[group] = append(s.groupIdx[group], value)
	if value.Default {
		if _, ok := s.ls[group]; !ok {
			v := *value
			v.Default = false
			v.Name = group
			v.Description = "Alias for target " + value.Name
			s.ls[group] = &v
			s.groupIdx[group] = append(s.groupIdx[group], &v)
		}
		if s.defaultTarget == nil {
			s.defaultTarget = value
		}
	}
	s.lastTarget = value
}

func (s *targetList) AddBuiltin(group string, value *targetWrapper) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if value.Default {
		panic("builtin targets cannot be default")
	}

	name := path.Join(group, value.Name)
	if _, ok := s.builtins[name]; ok {
		panic("builtin target already exists: " + name)
	}

	if _, ok := s.ls[name]; ok {
		panic("registered target with same name already exists: " + name)
	}
	value.Name = name
	s.builtins[name] = value
}

func (s *targetList) Get(name string) *targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ls[name]
}

func (s *targetList) All() []*targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	ls := make([]*targetWrapper, 0, len(s.ls))
	for _, t := range s.ls {
		ls = append(ls, t)
	}
	for _, t := range s.builtins {
		ls = append(ls, t)
	}

	sort.Slice(ls, func(i, j int) bool {
		return ls[i].Name < ls[j].Name
	})

	return ls
}

func (s *targetList) GetGroup(group string) []*targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.groupIdx[group]
}

func (s *targetList) Builtin() []*targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	ls := make([]*targetWrapper, 0, len(s.builtins))
	for _, t := range s.builtins {
		ls = append(ls, t)
	}
	return ls
}

func (s *targetList) Default() *targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.defaultTarget != nil {
		return s.defaultTarget
	}
	return s.lastTarget
}

var registeredTargets = &targetList{
	ls:       make(map[string]*targetWrapper),
	groupIdx: make(map[string][]*targetWrapper),
	builtins: make(map[string]*targetWrapper),
}

// RegisterTarget registers a target.
// The default target is determined by the order in which targets are registered.
// The first target which has Default=true is the default target.
// This can be changed by calling [SetDefault].
//
// Registered targets may be overridden by targets from a [dalec.Spec].
func RegisterTarget(group string, t *bktargets.Target, build BuildFunc) {
	registeredTargets.Add(group, &targetWrapper{Target: *t, Build: build})
}

// RegisterBuiltin registers a builtin target.
// This is similar to [RegisterTarget], but the target is not overridable by a [dalec.Spec].
func RegisterBuiltin(group string, t *bktargets.Target, build BuildFunc) {
	registeredTargets.AddBuiltin(group, &targetWrapper{Target: *t, Build: build})
}

// SetDefault sets the default target for when no target is specified.
func SetDefault(group, name string) {
	registeredTargets.mu.Lock()
	defer registeredTargets.mu.Unlock()

	t := registeredTargets.ls[group+"/"+name]
	if t == nil {
		panic("target not found: " + group + "/" + name)
	}
	t.Default = true

	registeredTargets.ls[group] = &targetWrapper{
		Target: bktargets.Target{
			Name:        group,
			Description: "Alias for target " + t.Name,
		},
	}
	registeredTargets.defaultTarget = t
}

func registerSpecTargets(ctx context.Context, spec *dalec.Spec, client gwclient.Client) error {
	var def *pb.Definition
	marshlSpec := func() (*pb.Definition, error) {
		if def != nil {
			return def, nil
		}

		dt, err := yaml.Marshal(spec)
		if err != nil {
			return nil, err
		}
		llbDef, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0o600, dt)).Marshal(ctx)
		if err != nil {
			return nil, err
		}
		def = llbDef.ToPB()
		return def, nil
	}

	opts := client.BuildOpts().Opts
	// Prevent infinite recursion in from forwarded builds
	// This means we only support 1 level of forwarding.
	// We could add a second opt to check for further nesting, but it is probably not worth it.
	switch opts[requestIDKey] {
	case bktargets.SubrequestsTargetsDefinition.Name:
		if _, ok := opts[dalecTargetOptKey]; ok {
			return nil
		}
	case dalecSubrequstForwardBuild:
		return nil
	}

	register := func(group string) error {
		t, ok := spec.Targets[group]
		if !ok {
			return nil
		}

		if t.Frontend == nil || t.Frontend.Image == "" {
			return nil
		}

		def, err := marshlSpec()
		if err != nil {
			return err
		}

		res, err := client.Solve(ctx, gwclient.SolveRequest{
			Frontend: "gateway.v0",
			FrontendInputs: map[string]*pb.Definition{
				"dockerfile": def,
			},
			FrontendOpt: map[string]string{
				requestIDKey:      bktargets.SubrequestsTargetsDefinition.Name,
				"source":          t.Frontend.Image,
				"cmdline":         t.Frontend.CmdLine,
				"frontend.caps":   "moby.buildkit.frontend.subrequests",
				dalecTargetOptKey: group,
			},
		})
		if err != nil {
			return errors.Wrapf(err, "error getting targets from frontend %q", t.Frontend.Image)
		}

		dt := res.Metadata["result.json"]
		var tl bktargets.List
		if err := json.Unmarshal(dt, &tl); err != nil {
			return errors.Wrapf(err, "error unmarshalling targets from frontend %q", t.Frontend.Image)
		}

		for _, bkt := range tl.Targets {
			// capture loop variables
			bkt := bkt
			t := t
			RegisterTarget(group, &bkt, makeTargetForwarder(t, &bkt))
		}
		return nil
	}

	// If we already have a target in the opts, only register that one.
	// ... unless this is a target list request, in which case we register all targets.
	if opts[requestIDKey] != bktargets.SubrequestsTargetsDefinition.Name {
		if t := opts["target"]; t != "" {
			return register(t)
		}
	}

	for group := range spec.Targets {
		if err := register(group); err != nil {
			return err
		}
	}
	return nil
}
