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
	rpmdistro "github.com/project-dalec/dalec/targets/linux/rpm/distro"
	"github.com/project-dalec/dalec/targets/linux/rpm/rockylinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/suse"
	"github.com/project-dalec/dalec/targets/windows"
)

const testingAltCacheIdentitySuffix = "testingalt"

type routeFunc func(prefix string, spec *dalec.Spec) ([]frontend.Route, error)
type testingAltCacheIdentityRouteFunc func(cacheIdentity string) routeFunc

func init() {
	registerDebRoutes(debian.TrixieDefaultTargetKey, debian.TrixieConfig)
	registerDebRoutes(debian.BookwormDefaultTargetKey, debian.BookwormConfig)
	registerDebRoutes(debian.BullseyeDefaultTargetKey, debian.BullseyeConfig)

	registerDebRoutes(ubuntu.BionicDefaultTargetKey, ubuntu.BionicConfig)
	registerDebRoutes(ubuntu.FocalDefaultTargetKey, ubuntu.FocalConfig)
	registerDebRoutes(ubuntu.JammyDefaultTargetKey, ubuntu.JammyConfig)
	registerDebRoutes(ubuntu.NobleDefaultTargetKey, ubuntu.NobleConfig)
	registerDebRoutes(ubuntu.ResoluteDefaultTargetKey, ubuntu.ResoluteConfig)

	registerRpmRoutes(almalinux.V8TargetKey, almalinux.ConfigV8)
	registerRpmRoutes(almalinux.V9TargetKey, almalinux.ConfigV9)

	registerRpmRoutes(rockylinux.V8TargetKey, rockylinux.ConfigV8)
	registerRpmRoutes(rockylinux.V9TargetKey, rockylinux.ConfigV9)

	registerRpmRoutes(azlinux.AzLinux3TargetKey, azlinux.Azlinux3Config)
	registerRpmRoutes(azlinux.AzLinux4TargetKey, azlinux.Azlinux4Config)

	registerRpmRoutes(suse.SLES15TargetKey, suse.ConfigSLES15)

	registerRoutes(flatcar.TargetKey, flatcar.DefaultConfig.Routes)

	registerWindowsRoutes(windows.DefaultTargetKey)
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

func registerRoutesWithTestingAltCacheIdentity(name string, routes routeFunc, cacheIdentity string, altRoutes testingAltCacheIdentityRouteFunc) {
	targets.RegisterRouteProvider(name, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return routes(name, spec)
	})

	if !includeAltTestingTargets {
		return
	}

	altName := targets.TestingAltTargetKey(name)
	testingAltRoutes := altRoutes(cacheIdentity + testingAltCacheIdentitySuffix)
	targets.RegisterRouteProvider(altName, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return testingAltRoutes(altName, spec)
	})
}

func registerDebRoutes(name string, cfg *debdistro.Config) {
	registerRoutesWithTestingAltCacheIdentity(name, cfg.Routes, cfg.BuildCacheIdentity(), func(cacheIdentity string) routeFunc {
		altCfg := *cfg
		altCfg.SetBuildCacheIdentity(cacheIdentity)
		return altCfg.Routes
	})
}

func registerRpmRoutes(name string, cfg *rpmdistro.Config) {
	registerRoutesWithTestingAltCacheIdentity(name, cfg.Routes, cfg.BuildCacheIdentity(), func(cacheIdentity string) routeFunc {
		altCfg := *cfg
		altCfg.SetBuildCacheIdentity(cacheIdentity)
		return altCfg.Routes
	})
}

func registerWindowsRoutes(name string) {
	registerRoutesWithTestingAltCacheIdentity(name, windows.Routes, windows.BuildCacheIdentity(), func(cacheIdentity string) routeFunc {
		return windows.RoutesWithCacheIdentity(cacheIdentity)
	})
}
