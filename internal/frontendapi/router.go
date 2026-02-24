package frontendapi

import (
	"context"

	"github.com/containerd/plugin"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/frontend/debug"
	"github.com/project-dalec/dalec/internal/plugins"
	debdistro "github.com/project-dalec/dalec/targets/linux/deb/distro"
	_ "github.com/project-dalec/dalec/targets/plugin"
)

const altSuffix = "-testing-alt"

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

		mux.Add(r.ID, v.(plugins.BuildHandler).Handle, nil)

		if includeAltTestingTargets {
			vv, ok := v.(*debdistro.Config)
			if ok {
				// WARNING: This is a nasty hack.
				// It changes the distro ID used for the alt distro
				// It does this because the deb implementation that uses cache dir's uses VersionID as a cache key
				// We need the cache keys to be different for these alt targets.
				// We should consider what we've done here as we iterate on our interfaces.
				deb := *vv
				deb.VersionID += "testingalt"
				v = &deb
			}
			mux.Add(TestingAltTargetKey(r.ID), v.(plugins.BuildHandler).Handle, nil)
		}
	}

	return nil
}

func TestingAltTargetKey(key string) string {
	return key + altSuffix
}
