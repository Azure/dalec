package dalec

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/util/sets"
)

type dependency struct {
	v    *vertex
	w    *vertex
	kind string
}
type cycle []*vertex
type cycles []cycle

type Graph struct {
	Specs    map[string]*Spec
	ordered  orderedDeps
	indices  map[string]int
	vertices []*vertex
	edges    sets.Set[dependency]
	m        sync.Mutex
}

type orderedDeps []*Spec

func (o orderedDeps) targetSlice(target string) []*Spec {
	for i, dep := range o {
		if dep.Name == target {
			return o[:i+1]
		}
	}
	return nil
}

func (g *Graph) OrderedSlice(target string) []*Spec {
	return g.ordered.targetSlice(target)
}

func (g *Graph) Len(target ...string) int {
	if len(target) == 0 {
		return len(g.ordered)
	}
	return len(g.ordered.targetSlice(target[0]))
}

type vertex struct {
	name    string
	index   *int
	lowlink int
	onStack bool
}

var (
	m          sync.Mutex
	BuildGraph *Graph
)

func (g *Graph) Last() *Spec {
	return g.ordered[len(g.ordered)-1]
}

func InitGraph(specs []*Spec) error {
	if BuildGraph != nil {
		return nil
	}

	BuildGraph = new(Graph)
	*BuildGraph = Graph{
		edges:    sets.New[dependency](),
		vertices: make([]*vertex, len(specs)),
		Specs:    make(map[string]*Spec),
		indices:  make(map[string]int),
	}
	BuildGraph.m.Lock()
	defer BuildGraph.m.Unlock()

	for i, spec := range specs {
		name := spec.Name
		BuildGraph.Specs[name] = spec
		v := &vertex{name: name}
		BuildGraph.indices[name] = i
		BuildGraph.vertices[i] = v
	}

	for name, spec := range BuildGraph.Specs {
		if spec.Dependencies == nil {
			continue
		}
		vi := BuildGraph.indices[name]
		v := BuildGraph.vertices[vi]
		type depMap struct {
			kind string
			m    map[string][]string
		}
		runtimeAndBuildDeps := []depMap{{m: spec.Dependencies.Build, kind: "build"}, {m: spec.Dependencies.Runtime, kind: "runtime"}}
		for _, deps := range runtimeAndBuildDeps {
			if deps.m == nil {
				continue
			}
			for dep, constraints := range deps.m {
				_ = constraints // TODO(pmengelbert)
				if name == dep {
					continue // ignore if cycle is length 1
				}
				wi, ok := BuildGraph.indices[dep]
				if !ok {
					// this is not one of ours
					continue
				}
				w := BuildGraph.vertices[wi]
				BuildGraph.edges.Insert(dependency{
					v:    v,
					w:    w,
					kind: deps.kind,
				})
			}
		}
	}

	return BuildGraph.topSort()
}

func (g *Graph) topSort() error {
	index := 0
	s := make([]*vertex, 0, len(g.vertices)+len(g.edges))
	push := func(i *vertex) {
		s = append(s, i)
	}

	// returns vertex and whether or not stack was empty
	pop := func() *vertex {
		l := len(s)
		if l == 0 {
			return nil
		}
		ret := s[l-1]
		s = s[:l-1]
		return ret
	}
	fmin := func(v, w int) int {
		if v <= w {
			return v
		}
		return w
	}

	output := cycles{}
	var strongConnect func(v *vertex)
	strongConnect = func(v *vertex) {
		v.index = new(int)
		*v.index = index
		v.lowlink = index
		index++
		push(v)
		v.onStack = true

		for edge := range g.edges {
			if v.name != edge.v.name {
				continue
			}
			w := edge.w
			if w.index == nil {
				strongConnect(w)
				v.lowlink = fmin(v.lowlink, v.lowlink)
				continue
			}
			if w.onStack {
				v.lowlink = fmin(v.lowlink, *w.index)
			}
		}

		if v.lowlink == *v.index {
			c := []*vertex{}
			var (
				w *vertex
			)
			for {
				w = pop()
				w.onStack = false
				c = append(c, w)
				if w == v {
					break
				}
			}
			w.onStack = false
			output = append(output, c)
		}
	}

	for _, v := range g.vertices {
		if v.index != nil {
			continue
		}
		strongConnect(v)
	}

	specs := make([]*Spec, 0, len(g.vertices))
	for _, components := range output {
		if len(components) > 1 {
			return fmt.Errorf("dalec dependency cycle: %s", components.disp())
		}
		for _, component := range components {
			specs = append(specs, g.Specs[component.name])
		}
	}

	g.ordered = specs
	return nil
}

func (c cycle) disp() string {
	if len(c) == 0 {
		return ""
	}
	s := c.String()
	s = s[:len(s)-2]
	return fmt.Sprintf("%s, %s }", s, c[0].name)
}

func (c cycle) String() string {
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

func (cs cycles) String() string {
	sb := strings.Builder{}
	for i, component := range cs {
		sb.WriteString(component.String())
		if i+1 == len(cs) {
			break
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
