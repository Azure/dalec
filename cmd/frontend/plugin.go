package main

import (
	"context"

	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/internal/plugins"
	"github.com/containerd/plugin"

	_ "github.com/project-dalec/dalec/targets/plugin"
)

func loadPlugins(ctx context.Context, mux *frontend.BuildMux) error {
	set := plugin.NewPluginSet()

	filter := func(r *plugins.Registration) bool {
		return r.Type != plugins.TypeBuildTarget
	}

	for _, r := range plugins.Graph(filter) {
		cfg := plugin.NewContext(ctx, set, nil)

		p := r.Init(cfg)
		if err := set.Add(p); err != nil {
			return err
		}

		v, err := p.Instance()
		if err != nil && !plugin.IsSkipPlugin(err) {
			return err
		}

		mux.Add(r.ID, v.(plugins.BuildHandler).HandleBuild, nil)
	}

	return nil
}
