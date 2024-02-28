package dalec

// `Project` is either a) A single spec or b) a list of specs nested
// under the `specs` key. In the case of the latter, the specs may
// or may not depend on one another.
type Project struct {
	*Spec     `json:",inline,omitempty" yaml:",inline,omitempty"`
	Frontends map[string]Frontend `json:"frontend,omitempty" yaml:"frontend,omitempty"`
	Specs     []Spec              `json:"specs,omitempty" yaml:"specs,omitempty"`
}

// This is a placeholder until it is implemented by PR #146
type Graph struct{}
