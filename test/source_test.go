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
		req := gwclient.SolveRequest{
			FrontendOpt: map[string]string{
				"target": "debug/sources",
			},
		}
		specToSolveRequest(ctx, t, spec, &req)

		res, err := gwc.Solve(ctx, req)
		if err != nil {
			return nil, err
		}

		checkFile(ctx, t, filepath.Join(sourceName, "foo"), res, []byte("foo bar\n"))
		checkFile(ctx, t, filepath.Join(sourceName, "hello"), res, []byte("hello\n"))

		return gwclient.NewResult(), nil
	})
}
