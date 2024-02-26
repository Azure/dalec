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
