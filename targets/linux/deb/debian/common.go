package debian

import (
	"context"

	"github.com/project-dalec/dalec/frontend"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

var (
	builderPackages = []string{
		"aptitude",
		"dpkg-dev",
		"devscripts",
		"equivs",
		"fakeroot",
		"dh-make",
		"build-essential",
		"dh-apparmor",
		"dh-make",
		"dh-exec",
	}

	// We want to install ca-certificates in the base image
	// to ensure that certain operations (such as fetching custom repo configs over https)
	// can be completed when the dalec-built packages are installed into the
	// base image.
	basePackages = []string{
		"ca-certificates",
	}

	targets = map[string]gwclient.BuildFunc{
		TrixieDefaultTargetKey: TrixieConfig.Handle,
		BookwormDefaultTargetKey: BookwormConfig.Handle,
		BullseyeDefaultTargetKey: BullseyeConfig.Handle,
	}
)

func Handlers(ctx context.Context, client gwclient.Client, m *frontend.BuildMux) error {
	return frontend.LoadBuiltinTargets(targets)(ctx, client, m)
}
