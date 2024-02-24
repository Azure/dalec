package test

import (
	"context"
	"testing"

	"github.com/Azure/dalec"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func TestSourceCmd(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)

	sourceName := "checkcmd"
	spec := &dalec.Spec{
		Args: map[string]string{
			"BAR": "bar",
		},
		Name: "cmd-source-ref",
		Sources: map[string]dalec.Source{
			sourceName: {
				Path: "/output",
				DockerImage: &dalec.SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &dalec.Command{
						Steps: []*dalec.BuildStep{
							{
								Command: `mkdir -p /output; echo "$FOO $BAR" > /output/foo`,
								Env: map[string]string{
									"FOO": "foo",
									"BAR": "$BAR", // make sure args are passed through
								},
							},
							// make sure state is preserved for multiple steps
							{
								Command: `echo "hello" > /output/hello`,
							},
							{
								Command: `cat /output/foo | grep "foo bar"`,
							},
						},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		req := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))
		res, err := gwc.Solve(ctx, req)
		if err != nil {
			return nil, err
		}

		checkFile(ctx, t, "foo", res, []byte("foo bar\n"))
		checkFile(ctx, t, "hello", res, []byte("hello\n"))

		return gwclient.NewResult(), nil
	})
}

func TestSourceBuild(t *testing.T) {
	t.Parallel()

	doBuildTest := func(t *testing.T, subTest string, spec *dalec.Spec) {
		t.Run(subTest, func(t *testing.T) {
			t.Parallel()

			testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
				ro := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))

				res, err := gwc.Solve(ctx, ro)
				if err != nil {
					return nil, err
				}
				checkFile(ctx, t, "hello", res, []byte("hello\n"))
				return gwclient.NewResult(), nil
			})
		})
	}

	const dockerfile = "FROM busybox\nRUN echo hello > /hello"

	newBuildSpec := func(p string, f func() dalec.Source) *dalec.Spec {
		return &dalec.Spec{
			Sources: map[string]dalec.Source{
				"test": {
					Path: "/hello",
					Build: &dalec.SourceBuild{
						DockerFile: p,
						Source:     f(),
					},
				},
			},
		}
	}

	t.Run("inline", func(t *testing.T) {
		fileSrc := func() dalec.Source {
			return dalec.Source{
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: dockerfile,
					},
				},
			}
		}
		dirSrc := func(p string) func() dalec.Source {
			return func() dalec.Source {
				return dalec.Source{
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								p: {
									Contents: dockerfile,
								},
							},
						},
					},
				}
			}
		}

		t.Run("unspecified build file path", func(t *testing.T) {
			doBuildTest(t, "file", newBuildSpec("", fileSrc))
			doBuildTest(t, "dir", newBuildSpec("", dirSrc("Dockerfile")))
		})

		t.Run("Dockerfile as build file path", func(t *testing.T) {
			doBuildTest(t, "file", newBuildSpec("Dockerfile", fileSrc))
			doBuildTest(t, "dir", newBuildSpec("Dockerfile", dirSrc("Dockerfile")))
		})

		t.Run("non-standard build file path", func(t *testing.T) {
			doBuildTest(t, "file", newBuildSpec("foo", fileSrc))
			doBuildTest(t, "dir", newBuildSpec("foo", dirSrc("foo")))
		})
	})
}
