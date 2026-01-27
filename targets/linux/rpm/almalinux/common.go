package almalinux

import (
	"context"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend"
)

var (
	builderPackages = []string{
		"binutils",
		"rpm-build",
		"ca-certificates",
	}

	targets = map[string]gwclient.BuildFunc{
		V8TargetKey: ConfigV8.Handle,
		V9TargetKey: ConfigV9.Handle,
	}

	defaultPlatformConfig = dalec.RepoPlatformConfig{
		ConfigRoot: "/etc/yum.repos.d",
		GPGKeyRoot: "/etc/pki/rpm-gpg",
		ConfigExt:  ".repo",
	}
)

func Handlers(ctx context.Context, client gwclient.Client, m *frontend.BuildMux) error {
	return frontend.LoadBuiltinTargets(targets)(ctx, client, m)
}

func basePackages(name string) []dalec.Spec {
	const (
		base    = "dalec-base-"
		license = "Apache-2.0"

		version = "0.0.1"
		rev     = "1"
	)

	return []dalec.Spec{
		{
			Name:        base + name,
			Version:     version,
			Revision:    rev,
			License:     license,
			Description: "DALEC base packages for " + name,
			Dependencies: &dalec.PackageDependencies{
				Runtime: dalec.PackageDependencyList{
					"almalinux-release": {},
					"tzdata":            {},
				},
			},
		},
	}
}
