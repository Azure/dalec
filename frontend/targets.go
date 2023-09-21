package frontend

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/azure/dalec"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
)

type BuildFunc func(ctx context.Context, client client.Client, spec *dalec.Spec) (client.Reference, *image.Image, error)

type targetWrapper struct {
	bktargets.Target
	Build BuildFunc
}

type targetList struct {
	mu            sync.Mutex
	ls            map[string]*targetWrapper
	defaultTarget *targetWrapper
	lastTarget    *targetWrapper
}

func (s *targetList) Add(group string, value *targetWrapper) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !strings.HasPrefix(value.Name+"/", group) {
		value.Name = path.Join(group, value.Name)
	}
	s.ls[value.Name] = value
	if value.Default {
		if _, ok := s.ls[group]; !ok {
			v := *value
			v.Default = false
			v.Name = group
			v.Description = "Alias for target " + value.Name
			s.ls[group] = &v
		}
		if s.defaultTarget == nil {
			s.defaultTarget = value
		}
	}
	s.lastTarget = value
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

	sort.Slice(ls, func(i, j int) bool {
		return ls[i].Name < ls[j].Name
	})

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

var registeredTargets = &targetList{ls: make(map[string]*targetWrapper)}

// RegisterTarget registers a target.
// The default target is determined by the order in which targets are registered.
// The first target which has Default=true is the default target.
// This can be changed by calling [SetDefault].
func RegisterTarget(distro string, t bktargets.Target, build BuildFunc) {
	registeredTargets.Add(distro, &targetWrapper{Target: t, Build: build})
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
