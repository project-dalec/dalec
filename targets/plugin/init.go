package plugin

import (
	"context"

	"github.com/project-dalec/dalec"
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
	registerRoutes(debian.TrixieDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return debian.TrixieConfig.Routes(debian.TrixieDefaultTargetKey, spec)
	})
	registerRoutes(debian.BookwormDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return debian.BookwormConfig.Routes(debian.BookwormDefaultTargetKey, spec)
	})
	registerRoutes(debian.BullseyeDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return debian.BullseyeConfig.Routes(debian.BullseyeDefaultTargetKey, spec)
	})

	registerRoutes(ubuntu.BionicDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return ubuntu.BionicConfig.Routes(ubuntu.BionicDefaultTargetKey, spec)
	})
	registerRoutes(ubuntu.FocalDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return ubuntu.FocalConfig.Routes(ubuntu.FocalDefaultTargetKey, spec)
	})
	registerRoutes(ubuntu.JammyDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return ubuntu.JammyConfig.Routes(ubuntu.JammyDefaultTargetKey, spec)
	})
	registerRoutes(ubuntu.NobleDefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return ubuntu.NobleConfig.Routes(ubuntu.NobleDefaultTargetKey, spec)
	})

	registerRoutes(almalinux.V8TargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return almalinux.ConfigV8.Routes(almalinux.V8TargetKey, spec)
	})
	registerRoutes(almalinux.V9TargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return almalinux.ConfigV9.Routes(almalinux.V9TargetKey, spec)
	})

	registerRoutes(rockylinux.V8TargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return rockylinux.ConfigV8.Routes(rockylinux.V8TargetKey, spec)
	})
	registerRoutes(rockylinux.V9TargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return rockylinux.ConfigV9.Routes(rockylinux.V9TargetKey, spec)
	})

	registerRoutes(azlinux.Mariner2TargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return azlinux.Mariner2Config.Routes(azlinux.Mariner2TargetKey, spec)
	})
	registerRoutes(azlinux.AzLinux3TargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return azlinux.Azlinux3Config.Routes(azlinux.AzLinux3TargetKey, spec)
	})

	registerRoutes(windows.DefaultTargetKey, func(_ context.Context, spec *dalec.Spec) ([]frontend.Route, error) {
		return windows.Routes(windows.DefaultTargetKey, spec)
	})
}

func registerRoutes(name string, routes func(ctx context.Context, spec *dalec.Spec) ([]frontend.Route, error)) {
	targets.RegisterRouteProvider(name, routes)
}
