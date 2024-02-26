package dalec

type Project struct {
	*Spec `json:",inline,omitempty" yaml:",inline,omitempty"`
	Specs []Spec `json:"specs" yaml:"specs"`
}
