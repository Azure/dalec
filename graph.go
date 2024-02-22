package dalec

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pmengelbert/stack"
	"golang.org/x/exp/constraints"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Graph struct {
	m          *sync.Mutex
	subArgs    *sync.Once
	setOrdered *sync.Once
	target     string
	specs      map[string]Spec
	ordered    []string // index into specs map
}

func (g *Graph) SubstituteArgs(allArgs map[string]map[string]string) error {
	g.m.Lock()
	defer g.m.Unlock()

	var err error

	g.subArgs.Do(func() {
		for name, args := range allArgs {
			s := g.specs[name]
			if err = s.SubstituteArgs(args); err != nil {
				return
			}
			g.specs[name] = s
		}
	})

	return err
}

type edge [2]*vertex

func (g *Graph) Target() Spec {
	return g.specs[g.target]
}

func (g *Graph) Get(name string) (Spec, bool) {
	s, ok := g.specs[name]
	return s, ok
}

// Ordered returns an array of Specs in dependency order, up to and
// including `target`. If `target` is the empty string, return the entire list.
// If the target is not found, return an empty array.
func (g *Graph) Ordered() []Spec {
	orderedSpecs := make([]Spec, 0, len(g.specs))
	for _, name := range g.ordered {
		spec := g.specs[name]
		orderedSpecs = append(orderedSpecs, spec)
		if name == g.target && g.target != "" {
			break
		}
	}

	return orderedSpecs
}

// Returns the length of the ordred list of dependencies, up to and including
// `target`. If `target` is the empty string, return the entire length.
func (g *Graph) Len() int {
	for i, name := range g.ordered {
		if name == g.target {
			return i + 1
		}
	}

	return len(g.ordered)
}

type vertex struct {
	name    string
	index   *int
	lowlink int
	onStack bool
}

type (
	GraphConfig struct{}
	GraphOpt    func(*GraphConfig) error
)

func NewGraph(specs []*Spec, subtarget, dalecTarget string, opts ...GraphOpt) (Graph, error) {
	cfg := GraphConfig{}

	g := Graph{
		m:          new(sync.Mutex),
		subArgs:    new(sync.Once),
		setOrdered: new(sync.Once),
		target:     subtarget,
		specs:      make(map[string]Spec),
	}

	vertices := make([]*vertex, len(specs))
	indices := make(map[string]int)
	edges := sets.New[edge]()

	for _, f := range opts {
		if err := f(&cfg); err != nil {
			return Graph{}, fmt.Errorf("error initializing graph: %w", err)
		}
	}

	// In case we decide to make the graph mutable further down the pipeline
	g.m.Lock()
	defer g.m.Unlock()

	for i, spec := range specs {
		if spec == nil {
			return Graph{}, fmt.Errorf("nil spec provided")
		}

		name := spec.Name
		g.specs[name] = *spec
		v := &vertex{name: name}
		indices[name] = i
		vertices[i] = v
	}

	if _, ok := g.specs[g.target]; !ok {
		return Graph{}, fmt.Errorf("subtarget %q not found", g.target)
	}

	group, _, ok := strings.Cut(dalecTarget, "/")
	if !ok {
		return Graph{}, fmt.Errorf("unable to extract group from target %q", dalecTarget)
	}

	for name, spec := range g.specs {
		buildDeps := getBuildDeps(&spec, group)
		runtimeDeps := getRuntimeDeps(&spec, group)

		if spec.Dependencies == nil {
			continue
		}

		vi := indices[name]
		v := vertices[vi]

		runtimeAndBuildDeps := [][]string{
			buildDeps,
			runtimeDeps,
		}

		for _, deps := range runtimeAndBuildDeps {
			if deps == nil {
				continue
			}

			for _, dep := range deps {
				if name == dep {
					continue // ignore if cycle is length 1
				}
				wi, ok := indices[dep]
				if !ok {
					// this is dependency in the package repo
					continue
				}
				w := vertices[wi]
				edges.Insert(edge{
					0: v,
					1: w,
				})
			}
		}
	}

	output := g.topSort(vertices, edges)

	if err := g.verify(output); err != nil {
		return Graph{}, err
	}

	g.setDepOrder(output, len(vertices))

	return g, nil
}

func (g *Graph) setDepOrder(connected [][]*vertex, length int) {
	g.setOrdered.Do(func() {
		specs := make([]string, 0, length)
		for _, components := range connected {
			for _, component := range components {
				specs = append(specs, component.name)
			}
		}

		g.ordered = specs
	})
}

// https://en.wikipedia.org/wiki/Tarjan%27s_strongly_connected_components_algorithm
func (g *Graph) topSort(vertices []*vertex, edges sets.Set[edge]) [][]*vertex {
	if g.ordered != nil {
		return nil
	}

	index := 0
	s := stack.New[*vertex]()
	output := [][]*vertex{}

	var strongConnect func(v *vertex)
	strongConnect = func(v *vertex) {
		v.index = new(int)
		*v.index = index
		v.lowlink = index
		index++

		s.Push(v)
		v.onStack = true

		for edge := range edges {
			if v.name != edge[0].name {
				continue
			}

			w := edge[1]
			if w.index == nil {
				strongConnect(w)

				v.lowlink = min(v.lowlink, v.lowlink)
				continue
			}

			if w.onStack {
				v.lowlink = min(v.lowlink, *w.index)
			}
		}

		if v.lowlink == *v.index {
			component := []*vertex{}

			var w *vertex
			isSome := func(o stack.Option[*vertex]) bool {
				if o.IsSome() {
					w = o.Unwrap()
					return true
				}
				return false
			}

			for opt := s.Pop(); isSome(opt); opt = s.Pop() {
				w.onStack = false
				component = append(component, w)
				if w == v {
					break
				}
			}

			w.onStack = false
			output = append(output, component)
		}
	}

	for _, v := range vertices {
		if v.index != nil {
			continue
		}

		strongConnect(v)
	}

	return output
}

func (g *Graph) verify(output [][]*vertex) error {
	for _, components := range output {
		if len(components) > 1 {
			return fmt.Errorf("dalec dependency cycle: %s", disp(components))
		}
	}

	return nil
}

func disp(c []*vertex) string {
	if len(c) == 0 {
		return ""
	}
	s := cycleString(c)
	s = s[:len(s)-2]
	return fmt.Sprintf("%s, %s }", s, c[0].name)
}

func cycleString(c []*vertex) string {
	sb := strings.Builder{}
	sb.WriteString("{ ")
	for i, elem := range c {
		sb.WriteString(elem.name)
		if i+1 == len(c) {
			break
		}
		sb.WriteString(", ")
	}
	sb.WriteString(" }")
	return sb.String()
}

func min[T constraints.Ordered](x, y T) T {
	if x < y {
		return x
	}

	return y
}

func getBuildDeps(spec *Spec, target string) []string {
	var deps *PackageDependencies
	if t, ok := spec.Targets[target]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = spec.Dependencies
		if deps == nil {
			return nil
		}
	}

	var out []string
	for p := range deps.Build {
		out = append(out, p)
	}

	sort.Strings(out)
	return out
}

func getRuntimeDeps(spec *Spec, target string) []string {
	var deps *PackageDependencies
	if t, ok := spec.Targets[target]; ok {
		deps = t.Dependencies
	}

	if deps == nil {
		deps = spec.Dependencies
		if deps == nil {
			return nil
		}
	}

	var out []string
	for p := range deps.Runtime {
		out = append(out, p)
	}

	sort.Strings(out)
	return out
}
