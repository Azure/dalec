package dalec

import (
	"fmt"
	"os"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"
	pkgyaml "cuelang.org/go/pkg/encoding/yaml"
	"github.com/google/go-cmp/cmp"
)

// assumes single cue file path
func withCueModule(t *testing.T, modPath string, runTest func(t *testing.T, cueSpec cue.Value)) {
	var c *cue.Context = cuecontext.New()
	contents, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatal(err)
	}

	val := c.CompileBytes(contents)
	if val.Err() != nil {
		t.Error(err)
	}

	runTest(t, val)
}

func sel(v cue.Value, fieldPath string) cue.Value {
	return v.LookupPath(cue.ParsePath(fieldPath))
}

var testHeader = `
name: "test-spec"
description: "Test Spec Header"
version: "0.1"
revision: 1
license: "Apache"
vendor: "Microsoft"
packager: "Azure Container Upstream"
`

func appendHeader(rest string) string {
	return testHeader + "\n" + rest

}

func assertYamlValidate(t *testing.T, marshalDef string, yaml string, wantMsgs []string) {
	withCueModule(t, "cue/spec.cue", func(t *testing.T, v cue.Value) {
		against := sel(v, marshalDef)
		fmt.Println(against)
		_, err := pkgyaml.Validate([]byte(yaml), against)

		errs := errors.Errors(err)
		msgs := []string{}
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}

		if !cmp.Equal(msgs, wantMsgs) {
			t.Fatalf("Unexpected errors: %v\n", cmp.Diff(errs, wantMsgs))
		}
	})
}

func TestCueValidate_CacheDirConfig(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		marshalDef string
		wantErrs   []string
	}{
		{
			name:       "invalid cache dir type",
			yaml:       `mode: "unknown-mode"`,
			marshalDef: "#CacheDirConfig",
			wantErrs: []string{
				"#CacheDirConfig.mode: 3 errors in empty disjunction:",
				`#CacheDirConfig.mode: conflicting values "locked" and "unknown-mode"`,
				`#CacheDirConfig.mode: conflicting values "private" and "unknown-mode"`,
				`#CacheDirConfig.mode: conflicting values "shared" and "unknown-mode"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.marshalDef, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_Sources(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		marshalDef string
		wantErrs   []string
	}{
		{
			name: "invalid source name character",
			yaml: appendHeader(
				`sources:
   mysrc$:
    git:
      url: "https://github.com/some-repo.git"`),
			marshalDef: "#Spec",
			wantErrs:   []string{"#Spec.sources.mysrc$: field not allowed"},
		},
		{
			name: "multiple source types defined",
			yaml: `
git:
  url: "https://github.com/some-repo.git"
http:
  url: "https://some-site/get-source"`,
			marshalDef: "#Source",
			wantErrs: []string{
				"#Source: 2 errors in empty disjunction:",
				"#Source.git: field not allowed",
				"#Source.http: field not allowed",
			},
		},
		{
			name:       "source: key provided, no sources defined",
			marshalDef: "#Spec",
			yaml: appendHeader(
				`sources:
`),
			wantErrs: []string{
				"#Spec.sources: conflicting values null and {[#sourceName]:#Source} (mismatched types null and struct)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.marshalDef, tt.yaml, tt.wantErrs)
		})
	}
}
