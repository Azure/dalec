package distro

import (
	"context"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func (cfg *Config) HandleRPM(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return nil, nil
}

func (cfg *Config) HandleSourcePkg(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	return nil, nil
}
