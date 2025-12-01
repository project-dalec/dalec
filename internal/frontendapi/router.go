package frontendapi

import (
	"context"

	"github.com/containerd/plugin"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/frontend/debug"
	"github.com/project-dalec/dalec/internal/plugins"
	_ "github.com/project-dalec/dalec/targets/plugin"
)

func NewBuildRouter(ctx context.Context) (*frontend.BuildMux, error) {
	var mux frontend.BuildMux
	mux.Add(debug.DebugRoute, debug.Handle, nil)

	if err := loadBuildPlugins(ctx, &mux); err != nil {
		return nil, err
	}
	return &mux, nil
}

func loadBuildPlugins(ctx context.Context, mux *frontend.BuildMux) error {
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
