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

type graph struct {
	target   string
	specs    map[string]*Spec
	ordered  []*Spec
	indices  map[string]int
	vertices []*vertex
	edges    sets.Set[dependency]
}

type dependency struct {
	v1 *vertex
	v2 *vertex
}

func (g *graph) Target() *Spec {
	return g.specs[g.target]
}

func (g *graph) Get(name string) (*Spec, bool) {
	s, ok := g.specs[name]
	return s, ok
}

// OrderedSlice returns an array of Specs in dependency order, up to and
// including `target`. If `target` is the empty string, return the entire list.
// If the target is not found, return an empty array.
func (g *graph) OrderedSlice(target string) []*Spec {
	if target == "" {
		return []*Spec(g.ordered)
	}

	for i, dep := range g.ordered {
		if dep.Name == target {
			return g.ordered[:i+1]
		}
	}

	return []*Spec{}
}

// Returns the length of the ordred list of dependencies, up to and including
// `target`. If `target` is the empty string, return the entire length.
func (g *graph) OrderedLen(target string) int {
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
	BuildGraph *graph
	NotFound   = errors.New("dependency not found")
)

func (g *graph) Last() *Spec {
	return g.ordered[len(g.ordered)-1]
}

func (g *graph) Lock() {
	graphLock.Lock()
}
func (g *graph) Unlock() {
	graphLock.Unlock()
}

func InitGraph(specs []*Spec, subtarget, dalecTarget string) error {
	if BuildGraph != nil {
		return nil
	}

	if BuildGraph == nil {
		BuildGraph = new(graph)
		BuildGraph.Lock()
		defer BuildGraph.Unlock()
		*BuildGraph = graph{
			target:   subtarget,
			edges:    sets.New[dependency](),
			vertices: make([]*vertex, len(specs)),
			specs:    make(map[string]*Spec),
			indices:  make(map[string]int),
			ordered:  nil,
		}
	}

	for i, spec := range specs {
		name := spec.Name
		BuildGraph.specs[name] = spec
		v := &vertex{name: name}
		BuildGraph.indices[name] = i
		BuildGraph.vertices[i] = v
	}

	if _, ok := BuildGraph.specs[BuildGraph.target]; !ok {
		return fmt.Errorf("subtarget %q not found", BuildGraph.target)
	}

	group, _, ok := strings.Cut(dalecTarget, "/")
	if !ok {
		return fmt.Errorf("unable to extract group from target %q", dalecTarget)
	}

	for name, spec := range BuildGraph.specs {
		buildDeps := getBuildDeps(spec, group)
		runtimeDeps := getRuntimeDeps(spec, group)
		if spec.Dependencies == nil {
			continue
		}
		vi := BuildGraph.indices[name]
		v := BuildGraph.vertices[vi]
		type depMap []string
		runtimeAndBuildDeps := []depMap{
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
				wi, ok := BuildGraph.indices[dep]
				if !ok {
					// this is dependency in the package repo
					continue
				}
				w := BuildGraph.vertices[wi]
				BuildGraph.edges.Insert(dependency{
					v1: v,
					v2: w,
				})
			}
		}
	}

	output := BuildGraph.topSort()

	if err := BuildGraph.verify(output); err != nil {
		return err
	}

	BuildGraph.setOrdered(output)

	return nil
}

func (g *graph) setOrdered(output [][]*vertex) {
	specs := make([]*Spec, 0, len(g.vertices))
	for _, components := range output {
		for _, component := range components {
			specs = append(specs, g.specs[component.name])
		}
	}

	g.ordered = specs
}

// https://en.wikipedia.org/wiki/Tarjan%27s_strongly_connected_components_algorithm
func (g *graph) topSort() [][]*vertex {
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

	for _, v := range g.vertices {
		if v.index != nil {
			continue
		}

		strongConnect(v)
	}

	return output
}

func (g *graph) verify(output [][]*vertex) error {
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
