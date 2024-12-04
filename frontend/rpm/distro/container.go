package distro

import (
	"context"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func (cfg *Config) HandleContainer(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	st := llb.Scratch().File(llb.Mkfile("dummy.txt", 0644, []byte("dummy content")))

	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	if err := ref.Evaluate(ctx); err != nil {
		return ref, err
	}

	return nil, nil
}
