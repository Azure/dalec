package dalec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

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
