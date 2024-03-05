package frontend

import (
	"context"
	"encoding/json"
	"fmt"
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
	ls             map[string]*targetWrapper
	groupIdx       map[string][]*targetWrapper
	defaultHandler *targetWrapper
	lastHandler    *targetWrapper
}

func (s *handlerList) Add(group string, value *targetWrapper) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !strings.HasPrefix(value.Name+"/", group) {
		value.Name = path.Join(group, value.Name)
	}

	s.ls[value.Name] = value
	s.groupIdx[group] = append(s.groupIdx[group], value)
	if value.Default {
		if s.defaultHandler == nil {
			s.defaultHandler = value
		}
	}
	s.lastHandler = value
}

func (s *handlerList) Get(name string) *targetWrapper {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ls[name]
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
	ls:       make(map[string]*targetWrapper),
	groupIdx: make(map[string][]*targetWrapper),
}

// RegisterHandler registers a target.
// The default target is determined by the order in which handlers are registered.
// The first handler which has Default=true is the default handler.
// This can be changed by calling [SetDefault].
//
// Registered handlers may be overridden by [dalec.Spec.Targets].
func RegisterHandler(group string, t bktargets.Target, build BuildFunc) {
	registeredHandlers.Add(group, &targetWrapper{Target: t, Build: build})
}

// SetDefault sets the default handler for when no handler is specified.
func SetDefault(group, name string) {
	registeredHandlers.mu.Lock()
	defer registeredHandlers.mu.Unlock()

	t := registeredHandlers.ls[group+"/"+name]
	if t == nil {
		panic("target not found: " + group + "/" + name)
	}
	t.Default = true

	registeredHandlers.ls[group] = &targetWrapper{
		Target: bktargets.Target{
			Name:        group,
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

	register := func(group, name string) error {
		project := wrapper

		grp, _, _ := strings.Cut(group, "/")
		t, ok := project.Frontends[grp]
		if !ok {
			bklog.G(ctx).WithField("group", group).Debug("No target found in forwarded build")
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
				dalecTargetOptKey: group,
				requestIDKey:      bktargets.SubrequestsTargetsDefinition.Name,
			},
		}

		if err := copyForForward(ctx, client, &req); err != nil {
			return err
		}

		caps := req.FrontendOpt["frontend.caps"]
		req.FrontendOpt["frontend.caps"] = strings.Join(append(strings.Split(caps, ","), "moby.buildkit.frontend.subrequests"), ",")

		bklog.G(ctx).WithField("group", group).WithField("target", t.Image).Debug("Requesting target list")
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
			// capture loop variables
			grp := strings.TrimSuffix(group, "/"+bkt.Name)
			if bkt.Name == group {
				grp = group
			}
			
			bklog.G(ctx).WithField("group", grp).WithField("target", bkt.Name).Debug("Registering forwarded target")
			fullName := grp
			if name != "" {
				fullName = fmt.Sprintf("%s/%s", name, group)
			}
		
			RegisterHandler(fullName, bkt, makeTargetForwarder(t, bkt))
		}

		if len(tl.Targets) == 0 {
			bklog.G(ctx).WithField("group", group).Debug("No targets found in forwarded build")
		}

		return nil
	}

	// If we already have a target in the opts, only register that one.
	// ... unless this is a target list request, in which case we register all targets.
	if opts[requestIDKey] != bktargets.SubrequestsTargetsDefinition.Name {
		if t := opts["target"]; t != "" {
			if wrapper.isSingleSpec {
				return register(t, "")
			}

			name, group, ok := strings.Cut(t, "/")
			if !ok {
				return fmt.Errorf("unexpected error, badly formatted target: %s", t)
			}
			err := register(group, name)
			if err != nil {
				return fmt.Errorf("here: %w, group: %s, name: %s", err, group, name)
			}
			return nil
		}
	}

	if wrapper.isSingleSpec {
		for group := range project.Spec.Targets {
			if err := register(group, ""); err != nil {
				return err
			}
		}
		return nil
	}

	for _, spec := range wrapper.GetSpecs() {
		name := spec.Name
		for group := range spec.Targets {
			if err := register(group, name); err != nil {
				return err
			}
		}
	}

	return nil
}
