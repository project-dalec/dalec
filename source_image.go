package dalec

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/solver/errdefs"
	"github.com/pkg/errors"
)

type SourceDockerImage struct {
	Ref string   `yaml:"ref" json:"ref"`
	Cmd *Command `yaml:"cmd,omitempty" json:"cmd,omitempty"`

	_sourceMap *sourceMap `yaml:"-" json:"-"`
}

// Command is used to execute a command to generate a source from a docker image.
type Command struct {
	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Steps is the list of commands to run to generate the source.
	// Steps are run sequentially and results of each step should be cached.
	Steps []*BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
}

func (cmd *Command) validate() error {
	if cmd == nil {
		return nil
	}

	var errs []error
	if len(cmd.Steps) == 0 {
		errs = append(errs, errors.New("command must have at least one step"))
	}

	for i, step := range cmd.Steps {
		if err := step.validate(); err != nil {
			errs = append(errs, fmt.Errorf("step %d: %w", i, err))
		}
	}

	if len(cmd.Mounts) > 0 {
		dests := make(map[string]struct{}, len(cmd.Mounts))
		for i, mount := range cmd.Mounts {
			if _, ok := dests[mount.Dest]; ok {
				errs = append(errs, fmt.Errorf("cmd mount %d: dest %q: duplicate mount destination", i, mount.Dest))
			}
			dests[mount.Dest] = struct{}{}
			if err := mount.validate(); err != nil {
				errs = append(errs, fmt.Errorf("cmd mount %d: dest %q: %w", i, mount.Dest, err))
			}
		}
	}

	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	return nil
}

// BuildStep is used to execute a command to build the artifact(s).
type BuildStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Mounts is the list of sources to mount into the build step.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	_sourceMap *sourceMap `json:"-" yaml:"-"`
}

func (step *BuildStep) validate() error {
	var errs []error

	if step.Command == "" {
		errs = append(errs, fmt.Errorf("step must have a command"))
	}

	if len(errs) > 0 {
		err := stderrors.Join(errs...)
		err = errdefs.WithSource(err, step._sourceMap.GetErrdefsSource())
		return err
	}

	return nil
}

func (step *BuildStep) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal BuildStep
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode build step")
	}

	*step = BuildStep(i)
	step._sourceMap = newSourceMap(ctx, node)
	return nil
}

// GetSourceLocation returns an llb.ConstraintsOpt representing the source map
// location for this BuildStep. It returns a no-op if there is no source map or
// the provided state is nil.
func (step *BuildStep) GetSourceLocation(state llb.State) (ret llb.ConstraintsOpt) {
	return step._sourceMap.GetLocation(state)
}

// SourceMount wraps a [Source] with a target mount point.
type SourceMount struct {
	// Dest is the destination directory to mount to
	Dest string `yaml:"dest" json:"dest" jsonschema:"required"`

	// Spec specifies the source to mount
	Spec Source `yaml:"spec" json:"spec" jsonschema:"required"`
}

func (s SourceMount) ToRunOption(sOpt SourceOpts, c llb.ConstraintsOpt) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		st, mountOpts := s.Spec.ToMount(sOpt, c)
		llb.AddMount(s.Dest, st, mountOpts...).SetRunOption(ei)
	})
}

func (m *SourceMount) validate() error {
	var errs []error
	if m.Dest == "/" {
		errs = append(errs, errors.Wrap(errInvalidMountConfig, "mount destination must not be \"/\""))
	}

	if err := m.Spec.validate(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	return nil
}

func (m *SourceMount) validateInRoot(root string) error {
	if err := m.validate(); err != nil {
		return err
	}
	if !isRoot(root) && pathHasPrefix(m.Dest, root) {
		// We cannot support this as the base mount for subPath will shadow the mount being done here.
		return errors.Wrapf(errInvalidMountConfig, "mount destination (%s) must not be a descendent of the target source path (%s)", m.Dest, root)
	}
	return nil
}

func (m *SourceMount) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	if err := m.Spec.processBuildArgs(lex, args, allowArg); err != nil {
		return errors.Wrapf(err, "mount dest: %s", m.Dest)
	}
	return nil
}

func (m *SourceMount) fillDefaults(gen []*SourceGenerator) {
	src := &m.Spec
	// TODO: need to pass in generators to fill defaults for the source
	src.fillDefaults()
	m.Spec = *src
}

var errNoImageSourcePath = stderrors.New("docker image source path cannot be empty")

func (src *SourceDockerImage) validate(opts fetchOptions) error {
	var errs []error
	if src.Ref == "" {
		err := errors.New("docker image source must have a ref")
		err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if src.Cmd != nil {
		if err := src.Cmd.validate(); err != nil {
			errs = append(errs, err)
		}
	}

	// If someone *really* wants to extract the entire rootfs, they need to say so explicitly.
	// We won't fill this in for them, particularly because this is almost certainly not the user's intent.
	if opts.Path == "" {
		err := errNoImageSourcePath
		err = errdefs.WithSource(err, src._sourceMap.GetErrdefsSource())
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}
	return nil
}

func (src *SourceDockerImage) IsDir() bool {
	return true
}

func (src *SourceDockerImage) baseState(opts fetchOptions) llb.State {
	base := llb.Image(
		src.Ref,
		llb.WithMetaResolver(opts.SourceOpt.Resolver),
		WithConstraints(opts.Constraints...),
		src._sourceMap.GetRootLocation(),
	)

	return base.With(src.Cmd.baseState(opts))
}

func (src *SourceDockerImage) toState(opts fetchOptions) llb.State {
	optsFiltered := opts
	if src.Cmd != nil {
		// If there is a command to run, then the content will already be filtered
		// down to opts.Path.
		optsFiltered.Path = "/"
	}

	return src.baseState(opts).With(sourceFilters(optsFiltered))
}

func (src *SourceDockerImage) toMount(opts fetchOptions) (llb.State, []llb.MountOption) {
	optsFiltered := opts
	var mountOpts []llb.MountOption
	if src.Cmd != nil {
		// If there is a command to run, then the content will already be filtered
		// down to opts.Path.
		optsFiltered.Path = "/"
		mountOpts = append(mountOpts, llb.SourcePath("/"))
	}

	return src.baseState(opts).With(mountFilters(optsFiltered)), mountOpts
}

func (cmd *Command) baseState(opts fetchOptions) llb.StateOption {
	return func(img llb.State) llb.State {
		if cmd == nil {
			return img
		}

		subPath := opts.Path
		if subPath == "" {
			// TODO: We should log a warning here since extracting an entire image while also running a command is
			// probably not what the user really wanted to do here.
			// The buildkit client provides functionality to do this we just need to wire it in.
			subPath = "/"
		}

		for k, v := range cmd.Env {
			img = img.AddEnv(k, v)
		}
		if cmd.Dir != "" {
			img = img.Dir(cmd.Dir)
		}

		baseRunOpts := make([]llb.RunOption, 0, len(cmd.Mounts))
		for _, mnt := range cmd.Mounts {
			if err := mnt.validateInRoot(subPath); err != nil {
				return asyncState(img, err)
			}
			baseRunOpts = append(baseRunOpts, mnt.ToRunOption(opts.SourceOpt, WithConstraints(opts.Constraints...)))
		}

		out := llb.Scratch()
		for i, step := range cmd.Steps {
			rOpts := []llb.RunOption{llb.Args([]string{"/bin/sh", "-c", step.Command})}

			rOpts = append(rOpts, baseRunOpts...)

			for k, v := range step.Env {
				rOpts = append(rOpts, llb.AddEnv(k, v))
			}

			for _, mnt := range step.Mounts {
				if err := mnt.validateInRoot(subPath); err != nil {
					return asyncState(img, err)
				}
				rOpts = append(rOpts, mnt.ToRunOption(opts.SourceOpt, WithConstraints(opts.Constraints...)))
			}

			opts := opts.Constraints

			v := img
			if i != 0 {
				v = out
			}
			opts = append(opts, step.GetSourceLocation(v))

			rOpts = append(rOpts, WithConstraints(opts...))
			cmdSt := img.Run(rOpts...)

			// on first iteration with a root subpath
			// do not use AddMount, as this will overwrite / with a
			// scratch fs
			if i == 0 && isRoot(subPath) {
				out = cmdSt.Root()
			} else {
				out = cmdSt.AddMount(subPath, out)
			}

			// Update the base state so that changes to the rootfs propagate between
			// steps.
			img = cmdSt.Root()
		}

		return out
	}
}

func (s *SourceDockerImage) fillDefaults(gen []*SourceGenerator) {
	if s == nil {
		return
	}
	if s.Cmd != nil {
		s.Cmd.fillDefaults(gen)
	}
}

func (s *Command) fillDefaults(gen []*SourceGenerator) {
	if s == nil {
		return
	}

	for i, mnt := range s.Mounts {
		m := &mnt
		m.fillDefaults(gen)
		s.Mounts[i] = *m
	}
}

func (src *SourceDockerImage) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	updated, err := expandArgs(lex, src.Ref, args, allowArg)
	if err != nil {
		errs = append(errs, fmt.Errorf("image ref: %w", err))
	}
	src.Ref = updated

	if src.Cmd != nil {
		if err := src.Cmd.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrap(err, "docker image cmd source"))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to process build args for docker image source: %w", stderrors.Join(errs...))
	}
	return nil
}

func (cmd *Command) doc(w io.Writer, name string) {
	if len(cmd.Env) > 0 {
		printDocLn(w, "	With the following environment variables set for all commands:")

		sorted := SortMapKeys(cmd.Env)
		for _, k := range sorted {
			printDocf(w, "		%s=%s\n", k, cmd.Env[k])
		}
	}
	if cmd.Dir != "" {
		printDocLn(w, "	Working Directory:", cmd.Dir)
	}

	printDocLn(w, "	Command(s):")
	for _, step := range cmd.Steps {
		printDocf(w, "		%s\n", step.Command)
		if len(step.Env) > 0 {
			printDocLn(w, "			With the following environment variables set for this command:")
			sorted := SortMapKeys(step.Env)
			for _, k := range sorted {
				printDocf(w, "				%s=%s\n", k, step.Env[k])
			}
		}
	}
	if len(cmd.Mounts) > 0 {
		printDocLn(w, "	With the following items mounted:")
		for _, src := range cmd.Mounts {
			printDocLn(w, "		Destination Path:", src.Dest)
			src.Spec.toInterface().doc(&indentWriter{w}, name)
		}
	}
}

func (src *SourceDockerImage) doc(w io.Writer, name string) {
	printDocLn(w, "Generated from a docker image:")
	printDocLn(w, "	Image:", src.Ref)

	if src.Cmd != nil {
		src.Cmd.doc(&indentWriter{w}, name)
	}
}
