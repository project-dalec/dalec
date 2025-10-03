package dalec

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/util/system"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var disableDiffMerge atomic.Bool

// DisableDiffMerge allows disabling the use of [llb.Diff] and [llb.Merge] in favor of [llb.Copy].
// This is needed when the buildkit version does not support [llb.Diff] and [llb.Merge].
//
// Mainly this would be to allow dockerd with the (current)
// standard setup of dockerd which uses "graphdrivers" to work as these ops are not
// supported by the graphdriver backend.
// When this is false and the graphdriver backend is used, the build will fail when buildkit
// checks the capabilities of the backend.
func DisableDiffMerge(v bool) {
	disableDiffMerge.Store(v)
}

type copyOptionFunc func(*llb.CopyInfo)

func (f copyOptionFunc) SetCopyOption(i *llb.CopyInfo) {
	f(i)
}

func WithIncludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.IncludePatterns = patterns
	})
}

func WithExcludes(patterns []string) llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.ExcludePatterns = patterns
	})
}

func WithDirContentsOnly() llb.CopyOption {
	return copyOptionFunc(func(i *llb.CopyInfo) {
		i.CopyDirContentsOnly = true
	})
}

type runOptionFunc func(*llb.ExecInfo)

func (f runOptionFunc) SetRunOption(i *llb.ExecInfo) {
	f(i)
}

// WithMountedAptCache gives an [llb.RunOption] that mounts the apt cache directories.
// It uses the given namePrefix as the prefix for the cache keys.
// namePrefix should be distinct per distro version.
func WithMountedAptCache(namePrefix string) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		// This is in the "official" docker image for ubuntu/debian.
		// This file prevents us from actually caching anything.
		// To resolve that we delete the file.
		ei.State = ei.State.File(
			llb.Rm("/etc/apt/apt.conf.d/docker-clean", llb.WithAllowNotFound(true)),
			ConstraintsOptFunc(func(c *llb.Constraints) {
				*c = ei.Constraints
			}),
		)

		llb.AddMount(
			"/var/cache/apt",
			llb.Scratch(),
			llb.AsPersistentCacheDir(namePrefix+"dalec-var-cache-apt", llb.CacheMountLocked),
		).SetRunOption(ei)

		llb.AddMount(
			"/var/lib/apt",
			llb.Scratch(),
			llb.AsPersistentCacheDir(namePrefix+"dalec-var-lib-apt", llb.CacheMountLocked),
		).SetRunOption(ei)
	})
}

func WithRunOptions(opts ...llb.RunOption) llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		for _, opt := range opts {
			opt.SetRunOption(ei)
		}
	})
}

type ConstraintsOptFunc func(*llb.Constraints)

func (f ConstraintsOptFunc) SetConstraintsOption(c *llb.Constraints) {
	f(c)
}

func (f ConstraintsOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(&ei.Constraints)
}

func (f ConstraintsOptFunc) SetLocalOption(li *llb.LocalInfo) {
	f(&li.Constraints)
}

func (f ConstraintsOptFunc) SetOCILayoutOption(oi *llb.OCILayoutInfo) {
	f(&oi.Constraints)
}

func (f ConstraintsOptFunc) SetHTTPOption(hi *llb.HTTPInfo) {
	f(&hi.Constraints)
}

func (f ConstraintsOptFunc) SetImageOption(ii *llb.ImageInfo) {
	f(&ii.Constraints)
}

func (f ConstraintsOptFunc) SetGitOption(gi *llb.GitInfo) {
	f(&gi.Constraints)
}

func WithConstraints(ls ...llb.ConstraintsOpt) llb.ConstraintsOpt {
	return ConstraintsOptFunc(func(c *llb.Constraints) {
		for _, opt := range ls {
			opt.SetConstraintsOption(c)
		}
	})
}

func WithConstraint(in *llb.Constraints) llb.ConstraintsOpt {
	return ConstraintsOptFunc(func(c *llb.Constraints) {
		if in == nil {
			return
		}
		*c = *in
	})
}

func withConstraints(opts []llb.ConstraintsOpt) llb.ConstraintsOpt {
	return WithConstraints(opts...)
}

// SortMapKeys is a convenience generic function to sort the keys of a map[string]T
func SortMapKeys[K cmp.Ordered, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func DuplicateMap[K comparable, V any](m map[K]V) map[K]V {
	newM := make(map[K]V, len(m))
	for k, v := range m {
		newM[k] = v
	}

	return newM
}

// MergeAtPath merges the given states into the given destination path in the given input state.
func MergeAtPath(input llb.State, states []llb.State, dest string, opts ...llb.ConstraintsOpt) llb.State {
	if disableDiffMerge.Load() {
		output := input
		for _, st := range states {
			output = output.
				File(llb.Copy(st, "/", dest, WithCreateDestPath()), opts...)
		}
		return output
	}

	diffs := make([]llb.State, 0, len(states)+1)
	diffs = append(diffs, input)

	for _, src := range states {
		st := src
		if dest != "" && dest != "/" {
			st = llb.Scratch().
				File(llb.Copy(src, "/", dest, WithCreateDestPath()), opts...)
		}
		diffs = append(diffs, llb.Diff(input, st, opts...))
	}
	return llb.Merge(diffs, opts...)
}

type localOptionFunc func(*llb.LocalInfo)

func (f localOptionFunc) SetLocalOption(li *llb.LocalInfo) {
	f(li)
}

func localIncludeExcludeMerge(includes []string, excludes []string) localOptionFunc {
	return func(li *llb.LocalInfo) {
		if len(excludes) > 0 {
			if li.ExcludePatterns != "" {
				var ls []string
				if err := json.Unmarshal([]byte(li.ExcludePatterns), &ls); err != nil {
					panic(err)
				}
				excludes = append(excludes, ls...)
			}
			llb.ExcludePatterns(excludes).SetLocalOption(li)
		}

		if len(includes) > 0 {
			if li.IncludePatterns != "" {
				var ls []string
				if err := json.Unmarshal([]byte(li.IncludePatterns), &ls); err != nil {
					panic(err)
				}
				includes = append(includes, ls...)
			}
			llb.IncludePatterns(includes).SetLocalOption(li)
		}
	}
}

type RunOptFunc func(*llb.ExecInfo)

func (f RunOptFunc) SetRunOption(ei *llb.ExecInfo) {
	f(ei)
}

// ProgressGroup creates a progress group with the given name.
// If a progress group is already set in the constraints the id is reused.
// If no progress group is set a new id is generated.
func ProgressGroup(name string) llb.ConstraintsOpt {
	return ConstraintsOptFunc(func(c *llb.Constraints) {
		if c.Metadata.ProgressGroup != nil {
			id := c.Metadata.ProgressGroup.Id
			llb.ProgressGroup(id, name, false).SetConstraintsOption(c)
			return
		}

		llb.ProgressGroup(identity.NewID(), name, false).SetConstraintsOption(c)
	})
}

func (s *Spec) GetImagePost(target string) *PostInstall {
	img := s.Targets[target].Image
	if img != nil {
		if img.Post != nil {
			return img.Post
		}
	}

	if s.Image != nil {
		return s.Image.Post
	}

	return nil
}

func (s *Spec) GetArtifacts(targetKey string) Artifacts {
	if t, ok := s.Targets[targetKey]; ok {
		// If unset then we should use the global artifacts but if set or deliberately empty then we should use that.
		if t.Artifacts != nil {
			return *t.Artifacts
		}
	}
	return s.Artifacts
}

// ShArgs returns a RunOption that runs the given command in a shell.
func ShArgs(args string) llb.RunOption {
	return llb.Args(append([]string{"sh", "-c"}, args))
}

// ShArgsf is the same as [ShArgs] but tkes a format string
func ShArgsf(format string, args ...interface{}) llb.RunOption {
	return ShArgs(fmt.Sprintf(format, args...))
}

// InstallPostSymlinks returns a RunOption that adds symlinks defined in the [PostInstall] underneath the provided rootfs path.
func InstallPostSymlinks(post *PostInstall, worker llb.State, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		const rootfsPath = "/tmp/rootfs"

		if post == nil {
			return in
		}

		if len(post.Symlinks) == 0 {
			return in
		}

		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex\n")

		sortedKeys := SortMapKeys(post.Symlinks)
		for _, oldpath := range sortedKeys {
			cfg := post.Symlinks[oldpath]
			newpaths := cfg.Paths
			sort.Strings(newpaths)

			for _, newpath := range newpaths {
				fmt.Fprintf(buf, "mkdir -p %q\n", filepath.Join(rootfsPath, filepath.Dir(newpath)))
				fmt.Fprintf(buf, "ln -s %q %q\n", oldpath, filepath.Join(rootfsPath, newpath))
				if cfg.User != "" {
					fmt.Fprintf(buf, "chown -h %s %q\n", cfg.User, filepath.Join(rootfsPath, newpath))
				}
				if cfg.Group != "" {
					fmt.Fprintf(buf, "chgrp -h %s %q\n", cfg.Group, filepath.Join(rootfsPath, newpath))
				}
			}
		}

		const name = "tmp.dalec.symlink.sh"
		script := llb.Scratch().File(llb.Mkfile(name, 0o700, buf.Bytes()))

		return worker.Run(
			ShArgs("/tmp/add_symlink.sh"),
			llb.AddMount("/tmp/add_symlink.sh", script, llb.SourcePath(name)),
			llb.AddMount("/etc/group", in, llb.SourcePath("/etc/group")),
			llb.AddMount("/etc/passwd", in, llb.SourcePath("/etc/passwd")),
			withConstraints(opts),
			ProgressGroup("Add post-install symlinks"),
		).AddMount(rootfsPath, in)
	}
}

func (s *Spec) GetSigner(targetKey string) (*PackageSigner, bool) {
	if s.Targets != nil {
		targetOverridesRootSigningConfig := hasValidSigner(s.PackageConfig)

		if t, ok := s.Targets[targetKey]; ok && hasValidSigner(t.PackageConfig) {
			return t.PackageConfig.Signer, targetOverridesRootSigningConfig
		}
	}

	if hasValidSigner(s.PackageConfig) {
		return s.PackageConfig.Signer, false
	}

	return nil, false
}

func hasValidSigner(pc *PackageConfig) bool {
	return pc != nil && pc.Signer != nil && pc.Signer.Image != ""
}

// SortMapValues is like [maps.Values], but the list is sorted based on the map key
func SortedMapValues[T any](m map[string]T) []T {
	keys := SortMapKeys(m)

	out := make([]T, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}

	return out
}

// MergeDependencies merges two sets of package dependencies, a base and a target.
// If a dependency is set in both, the one from `target` is used, otherwise, the dependency from parent is used.
// MergeDependencies(nil, child) = child, MergeDependencies(parent, nil) = parent
func MergeDependencies(base, target *PackageDependencies) *PackageDependencies {
	var (
		build      PackageDependencyList
		runtime    PackageDependencyList
		recommends PackageDependencyList
		test       PackageDependencyList
		extraRepos []PackageRepositoryConfig
	)

	if base == nil {
		return target
	}

	if target == nil {
		return base
	}

	if len(target.Build) > 0 {
		build = target.Build
	} else {
		build = base.Build
	}

	if len(target.Runtime) > 0 {
		runtime = target.Runtime
	} else {
		runtime = base.Runtime
	}

	if len(target.Recommends) > 0 {
		recommends = target.Recommends
	} else {
		recommends = base.Recommends
	}

	if len(target.Test) > 0 {
		test = target.Test
	} else {
		test = base.Test
	}

	if len(target.ExtraRepos) > 0 {
		extraRepos = target.ExtraRepos
	} else {
		extraRepos = base.ExtraRepos
	}

	return &PackageDependencies{
		Build:      build,
		Runtime:    runtime,
		Recommends: recommends,
		Test:       test,
		ExtraRepos: extraRepos,
	}
}

// GetPackageDeps returns the package dependencies for the given target.
// If the target does not have dependencies, the global dependencies are returned.
func (s *Spec) GetPackageDeps(target string) *PackageDependencies {
	if _, ok := s.Targets[target]; !ok {
		if s.Dependencies == nil {
			return &PackageDependencies{}
		}
		return s.Dependencies
	}
	return MergeDependencies(s.Dependencies, s.Targets[target].Dependencies)
}

type RepoPlatformConfig struct {
	ConfigRoot string
	GPGKeyRoot string
	ConfigExt  string
}

// Returns a run option which mounts the data dirs for all specified repos
func WithRepoData(repos []PackageRepositoryConfig, sOpts SourceOpts, opts ...llb.ConstraintsOpt) llb.RunOption {
	if len(repos) == 0 {
		return RunOptFunc(func(ei *llb.ExecInfo) {})
	}
	mounts := make([]llb.RunOption, 0, len(repos))
	for _, repo := range repos {
		mount := repoDataAsMount(repo, sOpts, opts...)
		mounts = append(mounts, mount)
	}

	return WithRunOptions(mounts...)
}

// Returns a run option for mounting the state (i.e., packages/metadata) for a single repo
func repoDataAsMount(config PackageRepositoryConfig, sOpts SourceOpts, opts ...llb.ConstraintsOpt) llb.RunOption {
	if len(config.Data) == 0 {
		return RunOptFunc(func(ei *llb.ExecInfo) {})
	}

	mounts := make([]llb.RunOption, 0, len(config.Data))
	for _, data := range config.Data {
		mounts = append(mounts, data.ToRunOption(sOpts, WithConstraints(opts...)))
	}

	return WithRunOptions(mounts...)
}

// SortedMap returns an iter that yields the keys and values of the map in sorted order based on the keys.
func SortedMapIter[K cmp.Ordered, V any](m map[K]V) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		keys := SortMapKeys(m)
		for _, k := range keys {
			if !yield(k, m[k]) {
				return
			}
		}
	}
}

func repoConfigAsMount(config PackageRepositoryConfig, platformCfg *RepoPlatformConfig, sOpt SourceOpts, opts ...llb.ConstraintsOpt) llb.RunOption {
	if len(config.Config) == 0 {
		return RunOptFunc(func(ei *llb.ExecInfo) {})
	}

	mounts := make([]llb.RunOption, 0, len(config.Config))
	for name, repoConfig := range SortedMapIter(config.Config) {
		normalized := name
		if filepath.Ext(normalized) != platformCfg.ConfigExt {
			normalized += platformCfg.ConfigExt
		}

		// each of these sources represent a repo config file
		to := filepath.Join(platformCfg.ConfigRoot, normalized)
		mnt, mountOpts := repoConfig.ToMount(sOpt, append(opts, ProgressGroup("Importing repo config: "+name))...)
		mounts = append(mounts, llb.AddMount(to, mnt, mountOpts...))
	}

	return WithRunOptions(mounts...)
}

// Returns a run option for importing the config files for all repos
func WithRepoConfigs(repos []PackageRepositoryConfig, cfg *RepoPlatformConfig, sOpt SourceOpts, opts ...llb.ConstraintsOpt) llb.RunOption {
	if len(repos) == 0 {
		return RunOptFunc(func(ei *llb.ExecInfo) {})
	}

	mounts := make([]llb.RunOption, 0, len(repos))
	for _, repo := range repos {
		mnt := repoConfigAsMount(repo, cfg, sOpt, opts...)
		mounts = append(mounts, mnt)
	}

	return WithRunOptions(mounts...)
}

func GetRepoKeys(configs []PackageRepositoryConfig, cfg *RepoPlatformConfig, sOpt SourceOpts, opts ...llb.ConstraintsOpt) (llb.RunOption, []string) {
	if len(configs) == 0 {
		return RunOptFunc(func(ei *llb.ExecInfo) {}), nil
	}

	mounts := make([]llb.RunOption, 0, len(configs))
	names := make([]string, 0, len(configs))
	for _, config := range configs {
		for name, repoKey := range config.Keys {
			mountPath := filepath.Join(cfg.GPGKeyRoot, name)
			st, mountOpts := repoKey.ToMount(sOpt, append(opts, ProgressGroup("Fetching repo key: "+name))...)
			mounts = append(mounts, llb.AddMount(mountPath, st, mountOpts...))
			names = append(names, name)
		}
	}

	return WithRunOptions(mounts...), names
}

const (
	netModeNone    = "none"
	netModeSandbox = "sandbox"
)

// SetBuildNetworkMode returns an [llb.StateOption] that determines which
func SetBuildNetworkMode(spec *Spec) llb.StateOption {
	switch spec.Build.NetworkMode {
	case "", netModeNone:
		return llb.Network(llb.NetModeNone)
	case netModeSandbox:
		return llb.Network(llb.NetModeSandbox)
	default:
		return func(in llb.State) llb.State {
			return in.Async(func(context.Context, llb.State, *llb.Constraints) (llb.State, error) {
				return in, fmt.Errorf("invalid build network mode %q", spec.Build.NetworkMode)
			})
		}
	}
}

// BaseImageConfig provides a default image config that can be used for
// producing images.
//
// This is taken from https://github.com/moby/buildkit/blob/0655923d7e2884a0d514313fd688178a6da57b43/frontend/dockerfile/dockerfile2llb/image.go#L26-L39
func BaseImageConfig(platform *ocispecs.Platform) *DockerImageSpec {
	img := &DockerImageSpec{}

	if platform == nil {
		p := platforms.DefaultSpec()
		platform = &p
	}

	img.Architecture = platform.Architecture
	img.OS = platform.OS
	img.OSVersion = platform.OSVersion
	if platform.OSFeatures != nil {
		img.OSFeatures = append([]string{}, platform.OSFeatures...)
	}
	img.Variant = platform.Variant
	img.RootFS.Type = "layers"
	img.Config.WorkingDir = "/"
	img.Config.Env = []string{"PATH=" + system.DefaultPathEnv(platform.OS)}

	return img
}

// Platform returns a [llb.ConstraintsOpt] that sets the platform to the provided platform
// If the platform is nil, the [llb.ConstraintOpt] is a no-op.
func Platform(platform *ocispecs.Platform) llb.ConstraintsOpt {
	if platform == nil {
		return ConstraintsOptFunc(func(c *llb.Constraints) {})
	}
	return llb.Platform(*platform)
}

func HasGolang(spec *Spec, targetKey string) bool {
	for dep := range spec.GetPackageDeps(targetKey).GetBuild() {
		switch dep {
		case "golang", "msft-golang":
			return true
		}
		if strings.HasPrefix(dep, "golang-") {
			return true
		}
	}
	return false
}

func (s *Spec) GetProvides(targetKey string) PackageDependencyList {
	if p := s.Targets[targetKey].Provides; p != nil {
		return p
	}
	return s.Provides
}

func (s *Spec) GetReplaces(targetKey string) PackageDependencyList {
	if r := s.Targets[targetKey].Replaces; r != nil {
		return r
	}
	return s.Replaces
}

func (s *Spec) GetConflicts(targetKey string) PackageDependencyList {
	if c := s.Targets[targetKey].Conflicts; c != nil {
		return c
	}
	return s.Conflicts
}

func HasNpm(spec *Spec, targetKey string) bool {
	for dep := range spec.GetPackageDeps(targetKey).GetBuild() {
		switch dep {
		case "npm":
			return true
		}
	}
	return false
}

// asyncState is a helper is useful when returning an error that can just be encapsulated in an async state.
// The error itself will propagate when the state once the state is marshalled (e.g. st.Marshal(ctx))
func asyncState(in llb.State, err error) llb.State {
	return in.Async(func(_ context.Context, in llb.State, _ *llb.Constraints) (llb.State, error) {
		return in, err
	})
}

type indentWriter struct {
	w io.Writer
}

func (w *indentWriter) Write(p []byte) (int, error) {
	scanner := bufio.NewScanner(bytes.NewReader(p))
	total := 0

	for scanner.Scan() {
		line := scanner.Text()
		_, err := w.w.Write([]byte("\t" + line + "\n"))
		if err != nil {
			return total, err
		}
		total += len(line)
	}

	if scanner.Err() != nil {
		return total, scanner.Err()
	}
	return total, nil
}

// ErrorState returns a state that contains the error in an async state.
// If the error is nil, it returns the input state unchanged.
func ErrorState(in llb.State, err error) llb.State {
	if err == nil {
		return in
	}
	return asyncState(in, err)
}

// NoopStateOption is a [llb.StateOption] that does not change the input state.
func NoopStateOption(in llb.State) llb.State {
	return in
}

// ErrorStateOption returns a [llb.StateOption] that returns a state option that
// surfaces the error in an async state.
// If the error is nil, it returns a no-op state option.
func ErrorStateOption(err error) llb.StateOption {
	if err == nil {
		return NoopStateOption
	}
	return func(in llb.State) llb.State {
		return asyncState(in, err)
	}
}

// GomodPatchCommandOptions configures platform-specific formatting for patch commands.
type GomodPatchCommandOptions struct {
	// UseInputFlag indicates whether to use --input flag (RPM style) instead of stdin redirect (DEB style).
	// RPM uses: patch --input file
	// DEB uses: patch < file
	UseInputFlag bool
}

// FormatGomodPatchCommand formats a shell command to apply a gomod patch.
// This centralizes the patch application logic used across RPM, DEB, and Windows targets.
//
// Parameters:
//   - patchPath: the filesystem path to the patch file
//   - targetDir: the directory to apply the patch in (source name)
//   - strip: the -p value to pass to patch
//   - opts: optional configuration (nil uses defaults: stdin redirect)
//
// The command includes a check to skip empty patches, which can occur when
// the replace/require directives result in no actual changes.
func FormatGomodPatchCommand(patchPath, targetDir string, strip int, opts *GomodPatchCommandOptions) string {
	if opts != nil && opts.UseInputFlag {
		// RPM style with --input flag
		return fmt.Sprintf("if [ -s %q ]; then patch -N -d %q -p%d -s --input %q; fi",
			patchPath, targetDir, strip, patchPath)
	}
	// DEB/default style with stdin redirect
	return fmt.Sprintf("if [ -s %q ]; then patch -N -d %q -p%d -s < %q; fi",
		patchPath, targetDir, strip, patchPath)
}

// GomodPatchSourceSet returns a merged set of source names that have either
// manual patches or gomod-generated patches. This is useful for iterating over
// all sources that need patch application.
func GomodPatchSourceSet(spec *Spec) map[string]struct{} {
	sources := make(map[string]struct{})
	for name := range spec.Patches {
		sources[name] = struct{}{}
	}
	for _, name := range spec.GomodPatchSources() {
		sources[name] = struct{}{}
	}
	return sources
}
