package frontend

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"fmt"
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
	"github.com/moby/buildkit/util/bklog"
	"github.com/pkg/errors"
)

const dalecTargetOptKey = "dalec.target"

type BuildFunc func(ctx context.Context, client gwclient.Client, spec *dalec.Spec) (gwclient.Reference, *image.Image, error)

type targetWrapper struct {
	bktargets.Target
	Build BuildFunc
}

type handlerList struct {
	mu             sync.Mutex
	ls             map[HandlerKey]*targetWrapper
	groupIdx       map[string][]*targetWrapper
	defaultHandler *targetWrapper
	lastHandler    *targetWrapper
}

// Additions to this struct must be name-string pairs.
type HandlerKey struct {
	Path     string
	Group    string
	SpecName string
}

func parseTarget(targetString string) (HandlerKey, error) {
	pairs := strings.Split(targetString, ",")

	var ret HandlerKey
	paths := 0
	for _, pair := range pairs {
		kv := strings.Split(pair, "=")
		if len(kv) == 1 {
			// i.e. this is the target path, make sure it's the only one
			paths++
			if paths > 1 {
				return HandlerKey{}, fmt.Errorf("target %q has multiple paths", targetString)
			}

			group, _, ok := strings.Cut(kv[0], "/")
			if !ok {
				return HandlerKey{}, fmt.Errorf("target %q has no group", targetString)
			}

			ret.Path = kv[0]
			ret.Group = group
			continue
		}

		k := kv[0]
		switch k {
		case "name":
			ret.SpecName = kv[1]
		default:
			return HandlerKey{}, fmt.Errorf("target key %q not recognized", k)
		}
	}

	if err := ret.validate(targetString); err != nil {
		return HandlerKey{}, err
	}

	return ret, nil
}

func (hk *HandlerKey) validate(targetString string) error {
	var errs error
	if hk.Group == "" {
		errs = goerrors.Join(errs, fmt.Errorf("target %q has no group %q", targetString, hk.Group))
	}
	if hk.Path == "" {
		errs = goerrors.Join(errs, fmt.Errorf("target %q has no path %q", targetString, hk.Path))
	}

	return errs
}

func (s *handlerList) Add(key HandlerKey, value *targetWrapper) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ls[key] = value
	s.groupIdx[key.Group] = append(s.groupIdx[key.Group], value)
	if value.Default {
		if s.defaultHandler == nil {
			s.defaultHandler = value
		}
	}
	s.lastHandler = value
}

func (s *handlerList) Get(key HandlerKey) *targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ls[key]
}

func (s *handlerList) All() []*targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	ls := make([]*targetWrapper, 0, len(s.ls))
	for _, t := range s.ls {
		ls = append(ls, t)
	}

	sort.Slice(ls, func(i, j int) bool {
		return ls[i].Name < ls[j].Name
	})

	return ls
}

func (s *handlerList) GetGroup(group string) []*targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.groupIdx[group]
}

func (s *handlerList) Default() *targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.defaultHandler != nil {
		return s.defaultHandler
	}
	return s.lastHandler
}

var registeredHandlers = &handlerList{
	ls:       make(map[HandlerKey]*targetWrapper),
	groupIdx: make(map[string][]*targetWrapper),
}

// RegisterHandler registers a target.
// The default target is determined by the order in which handlers are registered.
// The first handler which has Default=true is the default handler.
// This can be changed by calling [SetDefault].
//
// Registered handlers may be overridden by [dalec.Spec.Targets].
func RegisterHandler(key HandlerKey, t bktargets.Target, build BuildFunc) {
	registeredHandlers.Add(key, &targetWrapper{Target: t, Build: build})
}

// SetDefault sets the default handler for when no handler is specified.
func SetDefault(key HandlerKey) {
	registeredHandlers.mu.Lock()
	defer registeredHandlers.mu.Unlock()

	t := registeredHandlers.ls[key]
	if t == nil {
		panic("target not found: " + key.Group + "/" + key.Path)
	}
	t.Default = true

	registeredHandlers.ls[key] = &targetWrapper{
		Target: bktargets.Target{
			Name:        key.Group,
			Description: "Alias for target " + t.Name,
		},
	}
	registeredHandlers.defaultHandler = t
}

func registerProjectHandlers(ctx context.Context, wrapper *projectWrapper, client gwclient.Client) error {
	var def *pb.Definition
	project := wrapper.Project
	marshlProj := func() (*pb.Definition, error) {
		if def != nil {
			return def, nil
		}

		dt, err := yaml.Marshal(project)
		if err != nil {
			return nil, err
		}
		llbDef, err := llb.Scratch().File(llb.Mkfile("Dockerfile", 0600, dt)).Marshal(ctx)
		if err != nil {
			return nil, err
		}
		def = llbDef.ToPB()
		return def, nil
	}

	opts := client.BuildOpts().Opts
	// Prevent infinite recursion in from forwarded builds
	// This means we only support 1 level of forwarding.
	// We could add a second opt to check for further nesting, but it is probaly not worth it.
	switch opts[requestIDKey] {
	case bktargets.SubrequestsTargetsDefinition.Name:
		if _, ok := opts[dalecTargetOptKey]; ok {
			return nil
		}
	case dalecSubrequstForwardBuild:
		return nil
	}

	register := func(key HandlerKey) error {
		project := wrapper

		t, ok := project.Frontends[key.Group]
		if !ok {
			bklog.G(ctx).WithField("group", key.Group).Debug("No target found in forwarded build")
			return nil
		}

		if t.Image == "" {
			return nil
		}

		def, err := marshlProj()
		if err != nil {
			return err
		}

		req := gwclient.SolveRequest{
			Frontend: "gateway.v0",
			FrontendInputs: map[string]*pb.Definition{
				"dockerfile": def,
			},
			FrontendOpt: map[string]string{
				"source":          t.Image,
				"cmdline":         t.CmdLine,
				dalecTargetOptKey: key.Group,
				requestIDKey:      bktargets.SubrequestsTargetsDefinition.Name,
			},
		}

		if err := copyForForward(ctx, client, &req); err != nil {
			return err
		}

		caps := req.FrontendOpt["frontend.caps"]
		req.FrontendOpt["frontend.caps"] = strings.Join(append(strings.Split(caps, ","), "moby.buildkit.frontend.subrequests"), ",")

		bklog.G(ctx).WithField("group", key.Group).WithField("target", t.Image).Debug("Requesting target list")
		res, err := client.Solve(ctx, req)
		if err != nil {
			return errors.Wrapf(err, "error getting targets from frontend %q", t.Image)
		}

		dt := res.Metadata["result.json"]
		var tl bktargets.List
		if err := json.Unmarshal(dt, &tl); err != nil {
			return errors.Wrapf(err, "error unmarshalling targets from frontend %q", t.Image)
		}

		for _, bkt := range tl.Targets {
			if key.Path == "" {
				key.Path = bkt.Name
			}
			bklog.G(ctx).WithField("group", key.Group).WithField("target", bkt.Name).Debug("Registering forwarded target")
			RegisterHandler(key, bkt, makeTargetForwarder(t, bkt))
		}

		if len(tl.Targets) == 0 {
			bklog.G(ctx).WithField("group", key.Group).Debug("No targets found in forwarded build")
		}

		return nil
	}

	// If we already have a target in the opts, only register that one.
	// ... unless this is a target list request, in which case we register all targets.
	if opts[requestIDKey] != bktargets.SubrequestsTargetsDefinition.Name {
		if t := opts["target"]; t != "" {
			key, err := parseTarget(t)
			if err != nil {
				return fmt.Errorf("could not parse target: %w", err)
			}
			return register(key)
		}
	}

	for _, spec := range wrapper.GetSpecs() {
		name := spec.Name

		for grp := range spec.Targets {
			for hkey := range registeredHandlers.ls {
				if hkey.Group != grp {
					continue
				}

				k := HandlerKey{
					Path:     hkey.Path,
					Group:    hkey.Group,
					SpecName: name,
				}

				if err := register(k); err != nil {
					return err
				}
			}
		}
	}

	return nil
}
