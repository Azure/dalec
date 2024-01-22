package dalec

import (
	"fmt"
	"testing"
)

func newSrc() Source {
	return Source{
		DockerImage: nil,
		Git:         nil,
		HTTPS:       nil,
		Context:     nil,
		Build:       nil,
		Path:        "",
		Includes:    []string{},
		Excludes:    []string{},
	}
}

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
													Git:   &SourceGit{},
													HTTPS: &SourceHTTPS{},
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
