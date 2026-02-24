package plugin

import (
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/targets/linux/deb/debian"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
	"github.com/project-dalec/dalec/targets/linux/flatcar"
	"github.com/project-dalec/dalec/targets/linux/rpm/almalinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/azlinux"
	"github.com/project-dalec/dalec/targets/linux/rpm/rockylinux"
	"github.com/project-dalec/dalec/targets/windows"
)

func init() {
	targets.RegisterBuildTarget(debian.TrixieDefaultTargetKey, debian.TrixieConfig.Handle)
	targets.RegisterBuildTarget(debian.BookwormDefaultTargetKey, debian.BookwormConfig.Handle)
	targets.RegisterBuildTarget(debian.BullseyeDefaultTargetKey, debian.BullseyeConfig.Handle)

	targets.RegisterBuildTarget(ubuntu.BionicDefaultTargetKey, ubuntu.BionicConfig.Handle)
	targets.RegisterBuildTarget(ubuntu.FocalDefaultTargetKey, ubuntu.FocalConfig.Handle)
	targets.RegisterBuildTarget(ubuntu.JammyDefaultTargetKey, ubuntu.JammyConfig.Handle)
	targets.RegisterBuildTarget(ubuntu.NobleDefaultTargetKey, ubuntu.NobleConfig.Handle)

	targets.RegisterBuildTarget(almalinux.V8TargetKey, almalinux.ConfigV8.Handle)
	targets.RegisterBuildTarget(almalinux.V9TargetKey, almalinux.ConfigV9.Handle)

	targets.RegisterBuildTarget(rockylinux.V8TargetKey, rockylinux.ConfigV8.Handle)
	targets.RegisterBuildTarget(rockylinux.V9TargetKey, rockylinux.ConfigV9.Handle)

	targets.RegisterBuildTarget(azlinux.Mariner2TargetKey, azlinux.Mariner2Config.Handle)
	targets.RegisterBuildTarget(azlinux.AzLinux3TargetKey, azlinux.Azlinux3Config.Handle)

	targets.RegisterBuildTarget(flatcar.TargetKey, flatcar.DefaultConfig.Handle)

	targets.RegisterBuildTarget(windows.DefaultTargetKey, windows.Handle)
}
