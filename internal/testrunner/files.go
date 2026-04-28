package testrunner

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

func withFileChecks(test *dalec.TestSpec, opts ...ValidationOpt) llb.StateOption {
	if len(test.Files) == 0 {
		return dalec.NoopStateOption
	}

	outs := make([]llb.StateOption, 0, len(test.Files))
	for file, check := range test.Files {
		outs = append(outs, withFileCheck(file, &check, opts...)...)
	}
	return mergeStateOptions(outs, opts...)
}

func withFileCheck(file string, check *dalec.FileCheckOutput, opts ...ValidationOpt) []llb.StateOption {
	var outs []llb.StateOption

	outs = append(outs, checkFileNotExists.WithCheck(file, check, opts...))
	outs = append(outs, checkFileIsDir.WithCheck(file, check, opts...))
	outs = append(outs, checkFilePerms.WithCheck(file, check, opts...))
	outs = append(outs, checkFileLinkTarget.WithCheck(file, check, opts...))
	outs = append(outs, withCheckOutput(file, &check.CheckOutput, opts...)...)

	return outs
}
