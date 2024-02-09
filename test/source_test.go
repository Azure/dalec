package test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Azure/dalec"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func TestSources(t *testing.T) {
	t.Run("cmd source", testCmdSource)
	t.Run("source mount, extract path handled by source", testSourceMountPathHandledBySource)
	t.Run("source mount, extract path handled by mount", testSourceMountPathHandledByFilter)
}

func testCmdSource(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(t)

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

		checkFile(ctx, t, filepath.Join(sourceName, "foo"), res, []byte("foo bar\n"))
		checkFile(ctx, t, filepath.Join(sourceName, "hello"), res, []byte("hello\n"))

		return gwclient.NewResult(), nil
	})
}

func testSourceMountPathHandledBySource(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(t)

	sourceName := "checkcmd"
	spec := &dalec.Spec{
		Name: "cmd-source-ref",
		Sources: map[string]dalec.Source{
			sourceName: {
				Path: "/baz",
				DockerImage: &dalec.SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &dalec.Command{
						Mounts: []dalec.SourceMount{
							{
								Dest: "/dst",
								Spec: dalec.Source{
									Path: "/out",
									DockerImage: &dalec.SourceDockerImage{
										Ref: "busybox:latest",
										Cmd: &dalec.Command{
											Steps: []*dalec.BuildStep{
												{
													Command: "mkdir -p /out; echo 'test contents' > /out/file.txt",
												},
											},
										},
									},
								},
							},
						},
						Steps: []*dalec.BuildStep{
							{
								Command: `set -ex`,
							},
							{
								Command: "mkdir -p /baz",
							},
							{
								Command: `cp /dst/file.txt /baz`,
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

		checkFile(ctx, t, filepath.Join(sourceName, "file.txt"), res, []byte("test contents\n"))
		return gwclient.NewResult(), nil
	})
}

func testSourceMountPathHandledByFilter(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(t)

	sourceName := "checkcmd"
	spec := &dalec.Spec{
		Name: "cmd-source-ref",
		Sources: map[string]dalec.Source{
			sourceName: {
				Path: "/contents",
				DockerImage: &dalec.SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &dalec.Command{
						Mounts: []dalec.SourceMount{
							{
								Dest: "/dst",
								Spec: dalec.Source{
									Git: &dalec.SourceGit{
										URL: "https://github.com/cpuguy83/go-md2man.git",
									},
									Path: "md2man",
								},
							},
						},
						Steps: []*dalec.BuildStep{
							{
								Command: `set -ex`,
							},

							{
								Command: `cp /dst/* /contents`,
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

		statFile(ctx, t, filepath.Join(sourceName, "md2man.go"), res)
		return gwclient.NewResult(), nil
	})
}
