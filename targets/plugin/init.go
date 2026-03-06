package plugin

import (
	"context"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
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
	registerRoutes(debian.TrixieDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return debian.TrixieConfig.Routes(debian.TrixieDefaultTargetKey, ctx, client)
	})
	registerRoutes(debian.BookwormDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return debian.BookwormConfig.Routes(debian.BookwormDefaultTargetKey, ctx, client)
	})
	registerRoutes(debian.BullseyeDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return debian.BullseyeConfig.Routes(debian.BullseyeDefaultTargetKey, ctx, client)
	})

	registerRoutes(ubuntu.BionicDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return ubuntu.BionicConfig.Routes(ubuntu.BionicDefaultTargetKey, ctx, client)
	})
	registerRoutes(ubuntu.FocalDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return ubuntu.FocalConfig.Routes(ubuntu.FocalDefaultTargetKey, ctx, client)
	})
	registerRoutes(ubuntu.JammyDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return ubuntu.JammyConfig.Routes(ubuntu.JammyDefaultTargetKey, ctx, client)
	})
	registerRoutes(ubuntu.NobleDefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return ubuntu.NobleConfig.Routes(ubuntu.NobleDefaultTargetKey, ctx, client)
	})

	registerRoutes(almalinux.V8TargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return almalinux.ConfigV8.Routes(almalinux.V8TargetKey, ctx, client)
	})
	registerRoutes(almalinux.V9TargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return almalinux.ConfigV9.Routes(almalinux.V9TargetKey, ctx, client)
	})

	registerRoutes(rockylinux.V8TargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return rockylinux.ConfigV8.Routes(rockylinux.V8TargetKey, ctx, client)
	})
	registerRoutes(rockylinux.V9TargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return rockylinux.ConfigV9.Routes(rockylinux.V9TargetKey, ctx, client)
	})

	registerRoutes(azlinux.Mariner2TargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return azlinux.Mariner2Config.Routes(azlinux.Mariner2TargetKey, ctx, client)
	})
	registerRoutes(azlinux.AzLinux3TargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return azlinux.Azlinux3Config.Routes(azlinux.AzLinux3TargetKey, ctx, client)
	})

	registerRoutes(windows.DefaultTargetKey, func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error) {
		return windows.Routes(windows.DefaultTargetKey, ctx, client)
	})
}

func registerRoutes(name string, routes func(ctx context.Context, client gwclient.Client) ([]frontend.Route, error)) {
	targets.RegisterRouteProvider(name, routes)
}
