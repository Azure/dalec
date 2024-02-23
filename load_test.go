package dalec

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
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
							DockerFile: "/other/nonempty/Dockerfile/path",
							Source: Source{
								Git: &SourceGit{},
							},
						},
					},
					DockerFile: "/nonempty/Dockerfile/path",
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
					DockerFile: "/nonempty/Dockerfile/path",
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
					DockerFile: "/nonempty/Dockerfile/path",
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
