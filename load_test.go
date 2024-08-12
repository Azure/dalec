package dalec

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

//go:embed test/fixtures/unmarshall/source-inline.yml
var sourceInlineTemplate []byte

func TestSourceValidation(t *testing.T) {
	cases := []struct {
		title     string
		src       Source
		expectErr bool
	}{
		{
			title:     "has no valid source variant",
			src:       Source{},
			expectErr: true,
		},
		{
			title: "has multiple non-nil source variants",
			src: Source{
				DockerImage: &SourceDockerImage{
					Ref: "nonempty:latest",
				},
				Git: &SourceGit{},
			},
			expectErr: true,
		},
		{
			title:     "has multiple source types in docker-image command mount",
			expectErr: true,
			src: Source{
				DockerImage: &SourceDockerImage{
					Ref: "nonempty:latest",
					Cmd: &Command{
						Mounts: []SourceMount{{
							Dest: "",
							Spec: Source{
								DockerImage: &SourceDockerImage{
									Ref: "",
									Cmd: &Command{
										Mounts: []SourceMount{
											{
												Spec: Source{
													Git:  &SourceGit{},
													HTTP: &SourceHTTP{},
												},
											},
										},
									},
								},
							},
						}},
					},
				},
			},
		},
		{
			title:     "has no non-nil source type in docker-image command mount",
			expectErr: true,
			src: Source{
				DockerImage: &SourceDockerImage{
					Ref: "nonempty:latest",
					Cmd: &Command{
						Mounts: []SourceMount{{
							Dest: "",
							Spec: Source{},
						}},
					},
				},
			},
		},
		{
			title:     "has recursive build sources",
			expectErr: true,
			src: Source{
				Build: &SourceBuild{
					Source: Source{
						Build: &SourceBuild{
							DockerfilePath: "/other/nonempty/Dockerfile/path",
							Source: Source{
								Git: &SourceGit{},
							},
						},
					},
					DockerfilePath: "/nonempty/Dockerfile/path",
				},
			},
		},
		{
			title:     "has invalid build subsource",
			expectErr: true,
			src: Source{
				Build: &SourceBuild{
					Source: Source{
						DockerImage: &SourceDockerImage{
							Ref: "",
						},
					},
					DockerfilePath: "/nonempty/Dockerfile/path",
				},
			},
		},
		{
			title:     "has multiple layers of recursion with an error at the bottom",
			expectErr: true,
			src: Source{
				Build: &SourceBuild{
					Source: Source{
						DockerImage: &SourceDockerImage{
							Ref: "nonempty:latest",
							Cmd: &Command{
								Mounts: []SourceMount{
									{
										Dest: "/nonempty",
										Spec: Source{
											DockerImage: &SourceDockerImage{
												Ref: "",
											},
										},
									},
								},
							},
						},
					},
					DockerfilePath: "/nonempty/Dockerfile/path",
				},
			},
		},
		{
			title:     "has inline file and files set",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{},
					Dir:  &SourceInlineDir{},
				},
			},
		},
		{
			title:     "has path separator in inline nested file name",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					Dir: &SourceInlineDir{
						Files: map[string]*SourceInlineFile{
							"file/with/slash": {},
						},
					},
				},
			},
		},
		{
			title:     "inline dir has negative UID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					Dir: &SourceInlineDir{
						UID: -1,
					},
				},
			},
		},
		{
			title:     "inline dir has negative GID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					Dir: &SourceInlineDir{
						GID: -1,
					},
				},
			},
		},
		{
			title:     "inline file has negative UID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{
						UID: -1,
					},
				},
			},
		},
		{
			title:     "inline file has negative GID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{
						GID: -1,
					},
				},
			},
		},
		{
			title:     "inline file has path set",
			expectErr: true,
			src: Source{
				Path: "subpath",
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
			},
		},
		{
			title:     "has invalid genator config",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
				Generate: []*SourceGenerator{{}},
			},
		},
		{
			title:     "has valid genator",
			expectErr: false,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
				Generate: []*SourceGenerator{{Gomod: &GeneratorGomod{}}},
			},
		},
		{
			title:     "docker images with cmd source must specify a path to extract",
			expectErr: true,
			src: Source{
				Path: "",
				DockerImage: &SourceDockerImage{
					Ref: "notexists:latest",
					Cmd: &Command{
						Steps: []*BuildStep{
							{Command: ":"},
						},
					},
				},
			},
		},
		{
			title:     "cmd souce mount dest must not be /",
			expectErr: true,
			src: Source{
				Path: "/foo",
				DockerImage: &SourceDockerImage{
					Ref: "notexists:latest",
					Cmd: &Command{
						Steps: []*BuildStep{
							{Command: ":"},
						},
						Mounts: []SourceMount{
							{
								Dest: "/",
								Spec: Source{
									Inline: &SourceInline{
										File: &SourceInlineFile{},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			title:     "cmd source mount dest must not be a descendent of the extracted source path",
			expectErr: true,
			src: Source{
				Path: "/foo",
				DockerImage: &SourceDockerImage{
					Ref: "notexists:latest",
					Cmd: &Command{
						Mounts: []SourceMount{
							{
								Dest: "/foo",
								Spec: Source{
									Inline: &SourceInline{
										File: &SourceInlineFile{},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		title := fmt.Sprintf("source %s", tc.title)
		t.Run(title, func(tt *testing.T) {
			err := tc.src.validate()
			if tc.expectErr && err != nil {
				return
			}

			if err != nil {
				tt.Fatal(err)
			}

			if tc.expectErr {
				tt.Fatal("expected error, but received none")
			}
		})
	}
}

func TestSourceFillDefaults(t *testing.T) {
	cases := []struct {
		title  string
		before Source
		after  Source
	}{
		{
			title: "fills default context name when source type is context",
			before: Source{
				Context: &SourceContext{
					Name: "",
				},
				Path: ".",
			},
			after: Source{
				Context: &SourceContext{
					Name: "context",
				},
				Path: ".",
			},
		},
		{
			title: "sets nested defaults when source type is docker image",
			before: Source{
				DockerImage: &SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &Command{
						Dir: "/build",
						Mounts: []SourceMount{
							{
								Dest: "/build/test",
								Spec: Source{
									Context: &SourceContext{
										Name: "",
									},
									Path: ".",
								},
							},
						},
					},
				},
				Path: ".",
			},
			after: Source{
				DockerImage: &SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &Command{
						Dir: "/build",
						Mounts: []SourceMount{
							{
								Dest: "/build/test",
								Spec: Source{
									Context: &SourceContext{
										Name: "context",
									},
									Path: ".",
								},
							},
						},
					},
				},
				Path: ".",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		title := fmt.Sprintf("source %s", tc.title)
		t.Run(title, func(t *testing.T) {
			src := tc.before
			expected := tc.after

			if err := src.validate(); err != nil {
				t.Fatal(err)
			}
			spec := &Spec{
				Sources: map[string]Source{
					"test": src,
				},
			}

			spec.FillDefaults()
			filledSrc := spec.Sources["test"]

			if !reflect.DeepEqual(filledSrc, expected) {
				s, err := json.MarshalIndent(&src, "", "\t")
				if err != nil {
					t.Fatal(err)
				}

				e, err := json.MarshalIndent(&expected, "", "\t")
				if err != nil {
					t.Fatal(err)
				}

				t.Fatalf("\nactual: %s\n-------------\nexpected: %s", string(s), string(e))
			}

		})
	}
}

func TestSourceInlineUnmarshalling(t *testing.T) {
	// NOTE: not using text template yaml for this test
	// tabs seem to be illegal in yaml indentation
	// yaml unmarshalling with strict mode doesn't produce a great error message.
	spec, err := LoadSpec(sourceInlineTemplate)
	if err != nil {
		t.Fatal(err)
	}

	contents := "Hello world!"
	for k, v := range spec.Sources {
		t.Run(k, func(t *testing.T) {
			if v.Inline.File != nil {
				if v.Inline.File.Contents != contents {
					t.Fatalf("expected %s, got %s", contents, v.Inline.File.Contents)
				}

				expected := os.FileMode(0o644)
				if v.Inline.File.Permissions != expected {
					t.Fatalf("expected %O, got %O", expected, v.Inline.File.Permissions)
				}
			}

			if v.Inline.Dir != nil {
				expected := os.FileMode(0o755)
				if v.Inline.Dir.Permissions != expected {
					t.Fatalf("expected %O, got %O", expected, v.Inline.Dir.Permissions)
				}
			}
		})
	}
}

func TestSourceNameWithPathSeparator(t *testing.T) {
	spec := &Spec{
		Sources: map[string]Source{
			"forbidden/name": {
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
			},
		},
	}

	err := spec.Validate()
	if err == nil {
		t.Fatal("expected error, but received none")
	}

	var expected *InvalidSourceError
	if !errors.As(err, &expected) {
		t.Fatalf("expected %T, got %T", expected, err)
	}

	if expected.Name != "forbidden/name" {
		t.Error("expected error to contain source name")
	}

	if !errors.Is(err, sourceNamePathSeparatorError) {
		t.Errorf("expected error to be sourceNamePathSeparatorError, got: %v", err)
	}
}

func TestUnmarshal(t *testing.T) {
	t.Run("x-fields are stripped from spec", func(t *testing.T) {
		dt := []byte(`
sources:
  test:
    inline:
      file:
        contents: "Hello world!"
x-some-field: "some value"
x-some-other-field: "some other value"
X-capitalized-other-field: "some other value capitalized X key"
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		src, ok := spec.Sources["test"]
		if !ok {
			t.Fatal("expected source to be present")
		}

		if src.Inline == nil {
			t.Fatal("expected inline source to be present")
		}

		if src.Inline.File == nil {
			t.Fatal("expected inline file to be present")
		}

		const xContents = "Hello world!"
		if src.Inline.File.Contents != xContents {
			t.Fatalf("expected %q, got %s", xContents, src.Inline.File.Contents)
		}
	})

	t.Run("unknown fields cause parse error", func(t *testing.T) {
		dt := []byte(`
sources:
  test:
    noSuchField: "some value"
`)

		_, err := LoadSpec(dt)
		if err == nil {
			t.Fatal("expected error, but received none")
		}
	})
}

func TestSpec_SubstituteBuildArgs(t *testing.T) {
	spec := &Spec{}
	assert.NilError(t, spec.SubstituteArgs(nil))

	env := map[string]string{}
	assert.NilError(t, spec.SubstituteArgs(env))

	// some values we'll be using throughout the test
	const (
		foo            = "foo"
		bar            = "bar"
		argWithDefault = "some default value"
		plainOleValue  = "some plain old value"
	)

	env["FOO"] = foo
	err := spec.SubstituteArgs(env)
	assert.ErrorIs(t, err, errUnknownArg, "args not defined in the spec should error out")

	spec.Args = map[string]string{}

	spec.Args["FOO"] = ""
	assert.NilError(t, spec.SubstituteArgs(env))

	pairs := map[string]string{
		"FOO":      "$FOO",
		"BAR":      "$BAR",
		"WHATEVER": "$VAR_WITH_DEFAULT",
		"REGULAR":  plainOleValue,
	}
	spec.PackageConfig = &PackageConfig{
		Signer: &PackageSigner{
			Args: maps.Clone(pairs),
		},
	}
	spec.Targets = map[string]Target{
		"t1": {}, // nil signer
		"t2": {
			PackageConfig: &PackageConfig{
				Signer: &PackageSigner{
					Args: maps.Clone(pairs),
				},
			},
		},
	}

	env["BAR"] = bar
	assert.ErrorIs(t, err, errUnknownArg, "args not defined in the spec should error out")

	spec.Args["BAR"] = ""
	spec.Args["VAR_WITH_DEFAULT"] = argWithDefault

	assert.NilError(t, spec.SubstituteArgs(env))

	// Base package config
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["FOO"], foo))
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["BAR"], bar))
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["WHATEVER"], argWithDefault))
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["REGULAR"], plainOleValue))

	// targets
	assert.Check(t, cmp.Nil(spec.Targets["t1"].Frontend))
	assert.Check(t, cmp.Equal(spec.Targets["t2"].PackageConfig.Signer.Args["BAR"], bar))
	assert.Check(t, cmp.Equal(spec.Targets["t2"].PackageConfig.Signer.Args["WHATEVER"], argWithDefault))
	assert.Check(t, cmp.Equal(spec.Targets["t2"].PackageConfig.Signer.Args["REGULAR"], plainOleValue))
}

func TestBuildArgSubst(t *testing.T) {
	t.Run("value provided", func(t *testing.T) {
		dt := []byte(`
args:
  test:

build:
  steps:
    - command: echo $TEST
      env: 
        TEST: ${test}
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{
			"test": "test",
		})
		assert.NilError(t, err)
		assert.Equal(t, spec.Build.Steps[0].Env["TEST"], "test")
	})

	t.Run("default value", func(t *testing.T) {
		dt := []byte(`
args:
  test: "test"

build:
  steps:
    - command: echo $TEST
      env: 
        TEST: ${test}
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})
		assert.NilError(t, err)
		assert.Equal(t, spec.Build.Steps[0].Env["TEST"], "test")
	})

	t.Run("build arg undeclared", func(t *testing.T) {
		dt := []byte(`
args:

build:
  steps:
    - command: echo $TEST
      env: 
        TEST: ${test}
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})
		assert.ErrorContains(t, err, `error performing shell expansion on build step 0: error performing shell expansion on env var "TEST" for step 0: build arg "test" not declared`)
	})

	t.Run("builtin build arg", func(t *testing.T) {
		dt := []byte(`
args:

build:
  steps:
    - command: echo '$OS'
      env: 
        OS: ${TARGETOS}
`)
		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})
		assert.ErrorContains(t, err,
			`error performing shell expansion on build step 0: error performing shell expansion on env var "OS" for step 0: opt-in arg "TARGETOS" not present in args`)
	})
}

func Test_validatePatch(t *testing.T) {
	type testCase struct {
		name     string
		patchSrc Source
		subpath  bool
	}

	// Create a test case for each source type.
	// For each type we need to specify if it should have a subpath or not.
	cases := []testCase{
		{
			name:     "ineline file",
			patchSrc: Source{Inline: &SourceInline{File: &SourceInlineFile{}}},
			subpath:  false,
		},
		{
			name:     "inline dir",
			patchSrc: Source{Inline: &SourceInline{Dir: &SourceInlineDir{}}},
			subpath:  true,
		},
		{
			name:     "git",
			patchSrc: Source{Git: &SourceGit{}},
			subpath:  true,
		},
		{
			name:     "image",
			patchSrc: Source{DockerImage: &SourceDockerImage{}},
			subpath:  true,
		},
		{
			name:     "HTTP",
			patchSrc: Source{HTTP: &SourceHTTP{}},
			subpath:  false,
		},
		{
			name:     "context",
			patchSrc: Source{Context: &SourceContext{}},
			subpath:  true,
		},
		{
			name:     "build",
			patchSrc: Source{Build: &SourceBuild{}},
			subpath:  true,
		},
	}

	// For each case generate 2 tests: 1 with a subpath and 1 without
	// Use the subpath field in the test case to determine if the validation
	// should return an error.
	//
	// If subpath is false in the testcase but the test is passing in a subpath then
	// an error is expected.
	// Likewise when subpath is true but no subpath is given.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("subpath=true", func(t *testing.T) {
				ps := PatchSpec{Path: "/test"}
				err := validatePatch(ps, tc.patchSrc)
				if tc.subpath {
					assert.NilError(t, err)
					return
				}
				assert.ErrorIs(t, err, errPatchFileNoSubpath)
			})
			t.Run("subpath=false", func(t *testing.T) {
				ps := PatchSpec{}
				err := validatePatch(ps, tc.patchSrc)
				if tc.subpath {
					assert.ErrorIs(t, err, errPatchRequiresSubpath)
					return
				}
				assert.NilError(t, err)
			})
		})
	}
}
