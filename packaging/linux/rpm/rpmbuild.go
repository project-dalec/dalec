package rpm

import (
	"errors"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

type CacheInfo struct {
	TargetKey string
	Caches    []dalec.CacheConfig
}

// Builds an RPM and source RPM from a spec
//
// `topDir` is the rpmbuild top directory which should contain the SOURCES and SPECS directories along with any other necessary files
//
// `workerImg` is the image to use for the build
// It is expected to have rpmbuild and any other necessary build dependencies installed
//
// `specPath` is the path to the spec file to build relative to `topDir`
func Build(topDir, workerImg llb.State, specPath string, caches CacheInfo, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Build RPM"))
	return workerImg.Run(
		// some notes on these args:
		//  - _topdir is the directory where rpmbuild will look for SOURCES and SPECS
		//  - _srcrpmdir is the directory where rpmbuild will put the source RPM
		//  - _rpmdir is the directory where rpmbuild will put the RPM
		//  - --buildroot is the work directory where rpmbuild will build the RPM
		//  - -ba tells rpmbuild to build the source and binary RPMs
		//
		// TODO(cpuguy83): specPath would ideally never change.
		// We don't want to have to re-run this step just because the path changed, this should be based on inputs only (ie the content of the rpm spec, sources, etc)
		// The path should not be an input.
		dalec.ShArgs(`rpmbuild --define "_topdir /build/top" --define "_srcrpmdir /build/out/SRPMS" --define "_rpmdir /build/out/RPMS" --buildroot /build/tmp/work -ba `+specPath),
		llb.AddMount("/build/top", topDir),
		llb.AddMount("/build/tmp", llb.Scratch(), llb.Tmpfs()),
		llb.Dir("/build/top"),
		dalec.WithConstraints(opts...),
		dalec.RunOptFunc(func(ei *llb.ExecInfo) {
			opts := []dalec.CacheConfigOption{
				dalec.WithCacheDirConstraints(opts...),
			}
			for _, cache := range caches.Caches {
				cache.ToRunOption(workerImg, caches.TargetKey, opts...).SetRunOption(ei)
			}
		}),
	).
		AddMount("/build/out", llb.Scratch())
}

var errMissingRequiredField = errors.New("missing required field")

// ValidateSpec makes sure all the necessary fields are present in the spec to make rpmbuild work
// This validation is specific to rpmbuild.
func ValidateSpec(spec *dalec.Spec) (out error) {
	if spec.Name == "" {
		out = errors.Join(out, fmt.Errorf("%w: name", errMissingRequiredField))
	}
	if spec.Version == "" {
		out = errors.Join(out, fmt.Errorf("%w: version", errMissingRequiredField))
	}
	if spec.Revision == "" {
		out = errors.Join(out, fmt.Errorf("%w: revision", errMissingRequiredField))
	}
	if spec.Description == "" {
		out = errors.Join(out, fmt.Errorf("%w: description", errMissingRequiredField))
	}
	if spec.License == "" {
		out = errors.Join(out, fmt.Errorf("%w: license", errMissingRequiredField))
	}
	return out
}
