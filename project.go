package dalec

// `Project` is either a) A single spec or b) a list of specs nested
// under the `specs` key. In the case of the latter, the specs may or
// may not depend on one another.
type Project struct {
	// The spec, in the event that the project is a single spec
	*Spec `json:",inline,omitempty" yaml:",inline,omitempty"`

	// Args is the list of arguments that can be used for shell-style expansion in (certain fields of) the spec.
	// Any arg supplied in the build request which does not appear in this list will cause an error.
	// Attempts to use an arg in the spec which is not specified here will assume to be a literal string.
	// The map value is the default value to use if the arg is not supplied in the build request.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`

	// A map of target-group to Frontend configurations. This
	// applies to all specs in the file
	Frontends map[string]Frontend `json:"frontend,omitempty" yaml:"frontend,omitempty"`

	// The list of specs, in the event that the project contains
	// multiple specs
	Specs []Spec `json:"specs,omitempty" yaml:"specs,omitempty"`
}

// This is a placeholder until it is implemented by PR #146
type Graph struct{}
