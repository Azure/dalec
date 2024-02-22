package dalec

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/pmengelbert/stack"
	"golang.org/x/exp/constraints"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Graph struct {
	m       *sync.Mutex
	target  string
	specs   map[string]Spec
	ordered []Spec
	edges   sets.Set[dependency]
}

type SubFunc func(*Graph)

func (g *Graph) SubstituteArgs(allArgs map[string]map[string]string) error {
	g.m.Lock()
	defer g.m.Unlock()
	var once sync.Once
	var err error

	once.Do(func() {
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

type dependency struct {
	v1 *vertex
	v2 *vertex
}

func (g *Graph) Target() Spec {
	return g.specs[g.target]
}

func (g *Graph) Get(name string) (Spec, bool) {
	s, ok := g.specs[name]
	return s, ok
}

// OrderedSlice returns an array of Specs in dependency order, up to and
// including `target`. If `target` is the empty string, return the entire list.
// If the target is not found, return an empty array.
func (g *Graph) OrderedSlice(target string) []Spec {
	if target == "" {
		return []Spec(g.ordered)
	}

	for i, dep := range g.ordered {
		if dep.Name == target {
			return g.ordered[:i+1]
		}
	}

	return []Spec{}
}

// Returns the length of the ordred list of dependencies, up to and including
// `target`. If `target` is the empty string, return the entire length.
func (g *Graph) OrderedLen(target string) int {
	if target == "" {
		return len(g.ordered)
	}

	return len(g.OrderedSlice(target))
}

type vertex struct {
	name    string
	index   *int
	lowlink int
	onStack bool
}

var (
	graphLock  sync.Mutex
	BuildGraph *Graph
	NotFound   = errors.New("dependency not found")
)

func (g *Graph) Last() Spec {
	return g.ordered[len(g.ordered)-1]
}

type (
	GraphConfig struct{}
	GraphOpt    func(*GraphConfig) error
)

func InitGraph(specs []*Spec, subtarget, dalecTarget string) error {
	if BuildGraph != nil {
		return nil
	}

	g, err := NewGraph(specs, subtarget, dalecTarget)
	if err != nil {
		return err
	}

	BuildGraph = &g
	return nil
}

func NewGraph(specs []*Spec, subtarget, dalecTarget string, opts ...GraphOpt) (Graph, error) {
	cfg := GraphConfig{}

	g := Graph{
		m:      new(sync.Mutex),
		target: subtarget,
		edges:  sets.New[dependency](),
		specs:  make(map[string]Spec),
	}

	vertices := make([]*vertex, len(specs))
	indices := make(map[string]int)

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
				g.edges.Insert(dependency{
					v1: v,
					v2: w,
				})
			}
		}
	}

	output := g.topSort(vertices)

	if err := g.verify(output); err != nil {
		return Graph{}, err
	}

	g.setOrdered(output, len(vertices))

	return g, nil
}

func (g *Graph) setOrdered(output [][]*vertex, length int) {
	specs := make([]Spec, 0, length)
	for _, components := range output {
		for _, component := range components {
			specs = append(specs, g.specs[component.name])
		}
	}

	g.ordered = specs
}

// https://en.wikipedia.org/wiki/Tarjan%27s_strongly_connected_components_algorithm
func (g *Graph) topSort(vertices []*vertex) [][]*vertex {
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

		for edge := range g.edges {
			if v.name != edge.v1.name {
				continue
			}

			w := edge.v2
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
