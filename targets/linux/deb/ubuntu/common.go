package ubuntu

import (
	"context"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec/frontend"
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
		BionicDefaultTargetKey: BionicConfig.Handle, // 18.04
		FocalDefaultTargetKey:  FocalConfig.Handle,  // 20.04
		JammyDefaultTargetKey:  JammyConfig.Handle,  // 22.04
		NobleDefaultTargetKey:  NobleConfig.Handle,  // 24.04
	}
)

func Handlers(ctx context.Context, client gwclient.Client, m *frontend.BuildMux) error {
	return frontend.LoadBuiltinTargets(targets)(ctx, client, m)
}
