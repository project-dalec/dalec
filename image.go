package dalec

import (
	"context"
	goerrors "errors"
	"strings"

	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type DockerImageSpec = dockerspec.DockerOCIImage
type DockerImageConfig = dockerspec.DockerOCIImageConfig

// ImageConfig is the configuration for the output image.
// When the target output is a container image, this is used to configure the image.
type ImageConfig struct {
	// Entrypoint sets the image's "entrypoint" field.
	// This is used to control the default command to run when the image is run.
	Entrypoint string `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	// Cmd sets the image's "cmd" field.
	// When entrypoint is set, this is used as the default arguments to the entrypoint.
	// When entrypoint is not set, this is used as the default command to run.
	Cmd string `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	// Env is the list of environment variables to set in the image.
	Env []string `yaml:"env,omitempty" json:"env,omitempty"`
	// Labels is the list of labels to set in the image metadata.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	// Volumes is the list of volumes for the image.
	// Volumes instruct the runtime to bypass the any copy-on-write filesystems and mount the volume directly to the container.
	Volumes map[string]struct{} `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	// WorkingDir is the working directory to set in the image.
	// This sets the directory the container will start in.
	WorkingDir string `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	// StopSignal is the signal to send to the container to stop it.
	// This is used to stop the container gracefully.
	StopSignal string `yaml:"stop_signal,omitempty" json:"stop_signal,omitempty" jsonschema:"example=SIGTERM"`
	// Base is the base image to use for the output image.
	// This only affects the output image, not the intermediate build image.

	// Deprecated: Use [Bases] instead.
	Base string `yaml:"base,omitempty" json:"base,omitempty"`

	// Bases is used to specify a list of base images to build images for.  The
	// intent of allowing multiple bases is for cases, such as Windows, where you
	// may want to publish multiple versions of a base image in one image.
	//
	// Windows is the example here because of the way Windows works, the image
	// that the base is based off of must match the OS version of the host machine.
	// Therefore it is common to have multiple Windows images in one with a
	// different value for the os version field of the platform.
	//
	// For the most part implementations are not expected to support multiple base
	// images and may error out if multiple are specified.
	//
	// This should not be set if [Base] is also set.
	Bases []BaseImage `yaml:"bases,omitempty" json:"bases,omitempty"`

	// Post is the post install configuration for the image.
	// This allows making additional modifications to the container rootfs after the package(s) are installed.
	//
	// Use this to perform actions that would otherwise require additional tooling inside the container that is not relevant to
	// the resulting container and makes a post-install script as part of the package unnecessary.
	Post *PostInstall `yaml:"post,omitempty" json:"post,omitempty"`

	// User is the that the image should run as.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
}

type BaseImage struct {
	// Rootfs represents an image rootfs.
	Rootfs Source `yaml:"rootfs" json:"rootfs"`
}

// MergeImageConfig copies the fields from the source [ImageConfig] into the destination [image.Image].
// If a field is not set in the source, it is not modified in the destination.
// Envs from [ImageConfig] are merged into the destination [image.Image] and take precedence.
func MergeImageConfig(dst *DockerImageConfig, src *ImageConfig) error {
	if src == nil {
		return nil
	}

	if src.Entrypoint != "" {
		split, err := shlex.Split(src.Entrypoint)
		if err != nil {
			return errors.Wrap(err, "error splitting entrypoint into args")
		}
		dst.Entrypoint = split
		// Reset cmd as this may be totally invalid now
		// This is the same behavior as the Dockerfile frontend
		dst.Cmd = nil
	}
	if src.Cmd != "" {
		split, err := shlex.Split(src.Cmd)
		if err != nil {
			return errors.Wrap(err, "error splitting cmd into args")
		}
		dst.Cmd = split
	}

	if len(src.Env) > 0 {
		// Env is append only
		// If the env var already exists, replace it
		envIdx := make(map[string]int)
		for i, env := range dst.Env {
			// Extract the environment variable name (part before '=')
			varName, _, found := strings.Cut(env, "=")
			if found {
				envIdx[varName] = i
			} else {
				// Environment variable without '=' - use the whole string as key
				envIdx[env] = i
			}
		}

		for _, env := range src.Env {
			// Extract the environment variable name from the new env var
			varName, _, found := strings.Cut(env, "=")
			if !found {
				varName = env
			}

			if idx, ok := envIdx[varName]; ok {
				dst.Env[idx] = env
			} else {
				dst.Env = append(dst.Env, env)
			}
		}
	}

	if src.WorkingDir != "" {
		dst.WorkingDir = src.WorkingDir
	}
	if src.StopSignal != "" {
		dst.StopSignal = src.StopSignal
	}

	if src.User != "" {
		dst.User = src.User
	}

	for k, v := range src.Volumes {
		if dst.Volumes == nil {
			dst.Volumes = make(map[string]struct{}, len(src.Volumes))
		}
		dst.Volumes[k] = v
	}

	for k, v := range src.Labels {
		if dst.Labels == nil {
			dst.Labels = make(map[string]string, len(src.Labels))
		}
		dst.Labels[k] = v
	}

	return nil
}

func (i *ImageConfig) validate() error {
	if i == nil {
		return nil
	}

	var errs []error

	if i.Base != "" && len(i.Bases) > 0 {
		errs = append(errs, errors.New("cannot specify both image.base and image.bases"))
	}

	for i, base := range i.Bases {
		if err := base.validate(); err != nil && !errorIsOnly(err, errNoImageSourcePath) {
			errs = append(errs, errors.Wrapf(err, "bases[%d]", i))
		}
	}

	if err := i.Post.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "postinstall"))
	}

	return goerrors.Join(errs...)
}

func (i *ImageConfig) fillDefaults() {
	if i == nil {
		return
	}

	// s.Bases is a superset of s.Base, so migrate s.Base to s.Bases
	if i.Base != "" {
		i.Bases = append(i.Bases, BaseImage{
			Rootfs: Source{
				DockerImage: &SourceDockerImage{
					Ref: i.Base,
				},
			},
		})

		i.Base = ""
	}

	for _, bi := range i.Bases {
		bi.fillDefaults()
	}

	i.Post.normalizeSymlinks()
}

func (s *BaseImage) validate() error {
	if s.Rootfs.DockerImage == nil {
		// In the future we may support other source types but this adds a lot of complexity
		// that is currently unnecessary.
		return errors.New("rootfs currently only supports image source types")
	}
	if err := s.Rootfs.validate(); err != nil {
		return errors.Wrap(err, "rootfs")
	}
	return nil
}

func (p *PostInstall) validate() error {
	if p == nil {
		return nil
	}

	var errs []error

	if err := validateSymlinks(p.Symlinks); err != nil {
		errs = append(errs, err)
	}

	return errors.Wrap(goerrors.Join(errs...), "symlink")
}

func (s *BaseImage) fillDefaults() {
	rootfs := &s.Rootfs
	rootfs.fillDefaults()
	s.Rootfs = *rootfs
}

func (p *PostInstall) normalizeSymlinks() {
	if p == nil {
		return
	}

	// validation has already taken place
	for oldpath := range p.Symlinks {
		cfg := p.Symlinks[oldpath]
		if cfg.Path == "" {
			continue
		}

		cfg.Paths = append(cfg.Paths, cfg.Path)
		cfg.Path = ""
		p.Symlinks[oldpath] = cfg
	}
}

func (bi *BaseImage) ResolveImageConfig(ctx context.Context, sOpt SourceOpts, opt sourceresolver.Opt) ([]byte, error) {
	// In the future, *BaseImage may support other source types, but for now it only supports Docker images.
	//
	// Likewise we may support passing in a config separate from the requested image rootfs,
	// e.g. through a new field in *BaseImage, but for now we only support resolving the image config from the provided image reference.
	_, _, dt, err := sOpt.Resolver.ResolveImageConfig(ctx, bi.Rootfs.DockerImage.Ref, opt)
	return dt, err
}

func (bi *BaseImage) ToState(sOpt SourceOpts, opts ...llb.ConstraintsOpt) llb.State {
	if bi == nil {
		return llb.Scratch()
	}
	return bi.Rootfs.ToState("", sOpt, opts...)
}

func (s *Spec) GetImageBases(targetKey string) []BaseImage {
	if t, ok := s.Targets[targetKey]; ok && t.Image != nil {
		// note: this is intentionally only doing a nil check and *not* a length check
		// so that an empty list of bases can be used to override the default bases
		if t.Image.Bases != nil {
			return t.Image.Bases
		}
	}

	if s.Image == nil {
		return nil
	}
	return s.Image.Bases
}

// GetSingleBase looks up the base images to use for the targetKey and returns
// only the first entry.
// If there is more than 1 entry an error is returned.
// If there are no entries then both return values are nil.
func (s *Spec) GetSingleBase(targetKey string) (*BaseImage, error) {
	bases := s.GetImageBases(targetKey)
	if len(bases) > 1 {
		return nil, errors.New("multiple image bases, expected only one")
	}
	if len(bases) == 0 {
		return nil, nil
	}
	return &bases[0], nil
}
