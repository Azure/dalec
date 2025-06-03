package targets

import (
	"github.com/Azure/dalec/internal/plugins"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func RegisterBuildTarget(name string, build gwclient.BuildFunc) {
	plugins.Register(&plugins.Registration{
		ID:   name,
		Type: plugins.TypeBuildTarget,
		InitFn: func(*plugins.InitContext) (interface{}, error) {
			return plugins.BuildHandlerFunc(build), nil
		},
	})
}
