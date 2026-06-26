package plugin

import (
	"context"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux/deb/debian"
	debdistro "github.com/project-dalec/dalec/targets/linux/deb/distro"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
	"github.com/project-dalec/dalec/targets/linux/flatcar"
	"github.com/project-dalec/dalec/targets/linux/rpm/almalinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/azlinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/rockylinux"
	"github.com/project-dalec/dalec/targets/windows"
)

const testingAltVersionIDSuffix = "testingalt"

type routeFunc func(prefix string, spec *dalec.Spec) ([]frontend.Route, error)

func init() {
	registerDebRoutes(debian.TrixieDefaultTargetKey, debian.TrixieConfig)
	registerDebRoutes(debian.BookwormDefaultTargetKey, debian.BookwormConfig)
	registerDebRoutes(debian.BullseyeDefaultTargetKey, debian.BullseyeConfig)

	registerDebRoutes(ubuntu.BionicDefaultTargetKey, ubuntu.BionicConfig)
	registerDebRoutes(ubuntu.FocalDefaultTargetKey, ubuntu.FocalConfig)
	registerDebRoutes(ubuntu.JammyDefaultTargetKey, ubuntu.JammyConfig)
	registerDebRoutes(ubuntu.NobleDefaultTargetKey, ubuntu.NobleConfig)
	registerDebRoutes(ubuntu.ResoluteDefaultTargetKey, ubuntu.ResoluteConfig)

	registerRoutes(almalinux.V8TargetKey, almalinux.ConfigV8.Routes)
	registerRoutes(almalinux.V9TargetKey, almalinux.ConfigV9.Routes)

	registerRoutes(rockylinux.V8TargetKey, rockylinux.ConfigV8.Routes)
	registerRoutes(rockylinux.V9TargetKey, rockylinux.ConfigV9.Routes)

	registerRoutes(azlinux.AzLinux3TargetKey, azlinux.Azlinux3Config.Routes)
	registerRoutes(azlinux.AzLinux4TargetKey, azlinux.Azlinux4Config.Routes)

	registerRoutes(flatcar.TargetKey, flatcar.DefaultConfig.Routes)

	registerRoutes(windows.DefaultTargetKey, windows.Routes)
}

func registerRoutes(name string, routes routeFunc) {
	targets.RegisterRouteProvider(name, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return routes(name, spec)
	})

	if !includeAltTestingTargets {
		return
	}

	altName := targets.TestingAltTargetKey(name)
	targets.RegisterRouteProvider(altName, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return routes(altName, spec)
	})
}

func registerDebRoutes(name string, cfg *debdistro.Config) {
	targets.RegisterRouteProvider(name, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return cfg.Routes(name, spec)
	})

	if !includeAltTestingTargets {
		return
	}

	altName := targets.TestingAltTargetKey(name)
	altCfg := *cfg
	altCfg.VersionID += testingAltVersionIDSuffix

	targets.RegisterRouteProvider(altName, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return altCfg.Routes(altName, spec)
	})
}
