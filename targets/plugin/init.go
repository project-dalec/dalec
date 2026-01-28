package plugin

import (
	"github.com/project-dalec/dalec/internal/plugins"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux/deb/debian"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
	"github.com/project-dalec/dalec/targets/linux/rpm/almalinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/azlinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/rockylinux"
	"github.com/project-dalec/dalec/targets/windows"
)

func init() {
	targets.RegisterBuildTarget(debian.TrixieDefaultTargetKey, debian.TrixieConfig)
	targets.RegisterBuildTarget(debian.BookwormDefaultTargetKey, debian.BookwormConfig)
	targets.RegisterBuildTarget(debian.BullseyeDefaultTargetKey, debian.BullseyeConfig)

	targets.RegisterBuildTarget(ubuntu.BionicDefaultTargetKey, ubuntu.BionicConfig)
	targets.RegisterBuildTarget(ubuntu.FocalDefaultTargetKey, ubuntu.FocalConfig)
	targets.RegisterBuildTarget(ubuntu.JammyDefaultTargetKey, ubuntu.JammyConfig)
	targets.RegisterBuildTarget(ubuntu.NobleDefaultTargetKey, ubuntu.NobleConfig)

	targets.RegisterBuildTarget(almalinux.V8TargetKey, almalinux.ConfigV8)
	targets.RegisterBuildTarget(almalinux.V9TargetKey, almalinux.ConfigV9)

	targets.RegisterBuildTarget(rockylinux.V8TargetKey, rockylinux.ConfigV8)
	targets.RegisterBuildTarget(rockylinux.V9TargetKey, rockylinux.ConfigV9)

	targets.RegisterBuildTarget(azlinux.Mariner2TargetKey, azlinux.Mariner2Config)
	targets.RegisterBuildTarget(azlinux.AzLinux3TargetKey, azlinux.Azlinux3Config)

	targets.RegisterBuildTarget(windows.DefaultTargetKey, plugins.BuildHandlerFunc(windows.Handle))
}
