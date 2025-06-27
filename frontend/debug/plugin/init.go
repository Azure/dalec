package plugin

import (
	"github.com/Azure/dalec/frontend/debug"
	"github.com/Azure/dalec/internal/plugins"
)

func init() {
	plugins.Register(&plugins.Registration{
		Type: plugins.TypeBuildTarget,
		ID:   debug.DebugRoute,
		InitFn: func(_ *plugins.InitContext) (interface{}, error) {
			return plugins.BuildHandlerFunc(debug.Handle), nil
		},
	})
}
