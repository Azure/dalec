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

func assertYamlValidate(t *testing.T, unmarshalInto string, yaml string, wantMsgs []string) {
	withCueModule(t, "cue/spec.cue", func(t *testing.T, v cue.Value) {
		against := sel(v, unmarshalInto)
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
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "invalid cache dir type",
			yaml:          `mode: "unknown-mode"`,
			unmarshalInto: "#CacheDirConfig",
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
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_SourceInlineFile(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "uid < 0",
			yaml:          `uid: -1`,
			unmarshalInto: "#SourceInlineFile",
			wantErrs: []string{
				"#SourceInlineFile.uid: invalid value -1 (out of bound >=0)",
			},
		},
		{
			name:          "gid < 0",
			yaml:          `gid: -1`,
			unmarshalInto: "#SourceInlineFile",
			wantErrs: []string{
				"#SourceInlineFile.gid: invalid value -1 (out of bound >=0)",
			},
		},
		{
			name:          "invalid permissions",
			yaml:          `permissions: 999`,
			unmarshalInto: "#SourceInlineFile",
			wantErrs: []string{
				"#SourceInlineFile.permissions: invalid value 999 (out of bound <=511)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_SourceInlineDir(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "uid < 0",
			yaml:          `uid: -1`,
			unmarshalInto: "#SourceInlineDir",
			wantErrs: []string{
				"#SourceInlineDir.uid: invalid value -1 (out of bound >=0)",
			},
		},
		{
			name:          "gid < 0",
			yaml:          `gid: -1`,
			unmarshalInto: "#SourceInlineDir",
			wantErrs: []string{
				"#SourceInlineDir.gid: invalid value -1 (out of bound >=0)",
			},
		},
		{
			name:          "invalid permissions",
			yaml:          `permissions: 999`,
			unmarshalInto: "#SourceInlineDir",
			wantErrs: []string{
				"#SourceInlineDir.permissions: invalid value 999 (out of bound <=511)",
			},
		},
		{
			name: "invalid nested file name",
			yaml: `
files:
  my/file:
    contents: "some file contents"`,
			unmarshalInto: "#SourceInlineDir",
			wantErrs: []string{
				`#SourceInlineDir.files."my/file": field not allowed`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_SourceInline(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "file and dir variants both defined",
			unmarshalInto: "#SourceInline",
			yaml: `
inline:
    file: 
        contents: "some file contents"
    dir:
        files:
            file1:
                contents: "some file contents"
`,
			wantErrs: []string{
				"#SourceInline.inline: 2 errors in empty disjunction:",
				"#SourceInline.inline.dir: field not allowed",
				"#SourceInline.inline.file: field not allowed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_SourceBuild(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "recursive build",
			unmarshalInto: "#SourceBuild",
			yaml: `
source:
  build:
    source:
      inline:
        file:
          contents: "FROM scratch"`,
			wantErrs: []string{
				"#SourceBuild.source.build: field not allowed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_SourceMount(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "nested source mount",
			unmarshalInto: "#SourceMount",
			yaml: `
dest: "/app/sub1"
spec:
  image: 
   ref: "nested-docker-image"
   cmd:
    dir: "/app/sub2"
    mounts:
      - dest: "/app/sub2/sub3"
        spec:
          image:
            ref: "nested-docker-image"
            cmd:
              mounts:
                - dest: "/app/sub2/sub3/sub4"
                  spec:
                    http:
                        url: "http://myhost.file.txt"
              steps:
                - command: "echo hello nested world"    
    steps:
      - command: "echo hello nested world"
`,
			wantErrs: []string{},
		},
		{
			name:          "BUG: null spec in source mount",
			unmarshalInto: "#SourceMount",
			yaml: `
dest: "/app/sub1"
spec:
`,
			wantErrs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_FileCheckOutput(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "embedded check output",
			unmarshalInto: "#FileCheckOutput",
			yaml: `
contains:
  - "abcd"
  - "efgh"
starts_with: "abcd"
`,
			wantErrs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_PatchSpec(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name:          "invalid source name",
			unmarshalInto: "#PatchSpec",
			yaml: `
source: mysource*
strip: 1
`,
			wantErrs: []string{
				`#PatchSpec.source: invalid value "mysource*" (out of bound =~"^[a-zA-Z0-9_-|.]+$")`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_Sources(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name: "invalid source name character in sources",
			yaml: appendHeader(
				`sources:
   mysrc$:
    git:
      url: "https://github.com/some-repo.git"`),
			unmarshalInto: "#Spec",
			wantErrs:      []string{"#Spec.sources.mysrc$: field not allowed"},
		},
		{
			name: "multiple source types defined",
			yaml: `
git:
  url: "https://github.com/some-repo.git"
http:
  url: "https://some-site/get-source"`,
			unmarshalInto: "#Source",
			wantErrs: []string{
				"#Source: 2 errors in empty disjunction:",
				"#Source.git: field not allowed",
				"#Source.http: field not allowed",
			},
		},
		{
			name:          "source: key provided, no sources defined",
			unmarshalInto: "#Spec",
			yaml: appendHeader(
				`sources:
`),
			wantErrs: []string{
				"#Spec.sources: conflicting values null and {[sourceName]:#Source} (mismatched types null and struct)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}

func TestCueValidate_ChangelogEntry(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		unmarshalInto string
		wantErrs      []string
	}{
		{
			name: "invalid date",
			yaml: `
date: invalid-date
author: "John Doe"`,
			unmarshalInto: "#ChangelogEntry",
			wantErrs: []string{
				`#ChangelogEntry.date: invalid value "invalid-date" (does not satisfy time.Time): error in call to time.Time: invalid time "invalid-date"`,
			},
		},
		{
			name: "valid date",
			yaml: `
date: "2021-01-01T00:00:00Z"
author: "First Last"
`,
			unmarshalInto: "#ChangelogEntry",
			wantErrs:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertYamlValidate(t, tt.unmarshalInto, tt.yaml, tt.wantErrs)
		})
	}
}
