package plugin

import (
	"github.com/project-dalec/dalec/frontend"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux/deb/debian"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
	"github.com/project-dalec/dalec/targets/linux/rpm/almalinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/azlinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/rockylinux"
	"github.com/project-dalec/dalec/targets/windows"
)

func init() {
	registerRoutes("debian", func() []frontend.Route {
		var routes []frontend.Route
		routes = append(routes, debian.TrixieConfig.Routes(debian.TrixieDefaultTargetKey)...)
		routes = append(routes, debian.BookwormConfig.Routes(debian.BookwormDefaultTargetKey)...)
		routes = append(routes, debian.BullseyeConfig.Routes(debian.BullseyeDefaultTargetKey)...)
		return routes
	})

	registerRoutes("ubuntu", func() []frontend.Route {
		var routes []frontend.Route
		routes = append(routes, ubuntu.BionicConfig.Routes(ubuntu.BionicDefaultTargetKey)...)
		routes = append(routes, ubuntu.FocalConfig.Routes(ubuntu.FocalDefaultTargetKey)...)
		routes = append(routes, ubuntu.JammyConfig.Routes(ubuntu.JammyDefaultTargetKey)...)
		routes = append(routes, ubuntu.NobleConfig.Routes(ubuntu.NobleDefaultTargetKey)...)
		return routes
	})

	registerRoutes("almalinux", func() []frontend.Route {
		var routes []frontend.Route
		routes = append(routes, almalinux.ConfigV8.Routes(almalinux.V8TargetKey)...)
		routes = append(routes, almalinux.ConfigV9.Routes(almalinux.V9TargetKey)...)
		return routes
	})

	registerRoutes("rockylinux", func() []frontend.Route {
		var routes []frontend.Route
		routes = append(routes, rockylinux.ConfigV8.Routes(rockylinux.V8TargetKey)...)
		routes = append(routes, rockylinux.ConfigV9.Routes(rockylinux.V9TargetKey)...)
		return routes
	})

	registerRoutes("azlinux", func() []frontend.Route {
		var routes []frontend.Route
		routes = append(routes, azlinux.Mariner2Config.Routes(azlinux.Mariner2TargetKey)...)
		routes = append(routes, azlinux.Azlinux3Config.Routes(azlinux.AzLinux3TargetKey)...)
		return routes
	})

	registerRoutes("windows", func() []frontend.Route {
		return windows.Routes(windows.DefaultTargetKey)
	})
}

func registerRoutes(name string, routes func() []frontend.Route) {
	targets.RegisterRouteProvider(name, routes)
}
