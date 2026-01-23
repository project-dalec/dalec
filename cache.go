package dalec

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	cacheMountShared  = "shared"  // llb.CacheMountShared
	cacheMountLocked  = "locked"  // llb.CacheMountLocked
	cacheMountPrivate = "private" // llb.CacheMountPrivate
	cacheMountUnset   = ""

	BazelDefaultSocketID = "bazel-default" // Default ID for bazel socket

)

// getSccacheSource returns a Source for downloading and verifying sccache using SourceHTTP
func getSccacheSource(p *ocispecs.Platform) Source {
	// sccache version and download configuration
	const (
		sccacheVersion     = "v0.10.0"
		sccacheDownloadURL = "https://github.com/mozilla/sccache/releases/download"
	)

	// Determine architecture string and checksum based on platform
	var arch, checksum string
	switch {
	case p.Architecture == "amd64":
		arch = "x86_64-unknown-linux-musl"
		checksum = "1fbb35e135660d04a2d5e42b59c7874d39b3deb17de56330b25b713ec59f849b"
	case p.Architecture == "arm64":
		arch = "aarch64-unknown-linux-musl"
		checksum = "d6a1ce4acd02b937cd61bc675a8be029a60f7bc167594c33d75732bbc0a07400"
	case p.Architecture == "arm" && p.Variant == "v7":
		arch = "armv7-unknown-linux-musleabi"
		checksum = "ab7af4e5c78aa71f38145e7bed41dd944d99ce5a00339424d927f8bbd8b61b78"
	default:
		// Fallback to linux x64 for unsupported platforms
		arch = "x86_64-unknown-linux-musl"
		checksum = "1fbb35e135660d04a2d5e42b59c7874d39b3deb17de56330b25b713ec59f849b"
	}

	// Build download URL
	url := fmt.Sprintf("%s/%s/sccache-%s-%s.tar.gz", sccacheDownloadURL, sccacheVersion, sccacheVersion, arch)

	return Source{
		HTTP: &SourceHTTP{
			URL:    url,
			Digest: digest.Digest("sha256:" + checksum),
		},
	}
}

// CacheConfig configures a cache to use for a build.
type CacheConfig struct {
	// Dir specifies a generic cache directory configuration.
	Dir *CacheDir `json:"dir,omitempty" yaml:"dir,omitempty" jsonschema:"oneof_required=dir"`
	// GoBuild specifies a cache for Go's incremental build artifacts.
	// This should speed up repeated builds of Go projects.
	GoBuild *GoBuildCache `json:"gobuild,omitempty" yaml:"gobuild,omitempty" jsonschema:"oneof_required=gobuild"`
	// RustSCCache specifies a cache for Rust build artifacts.
	// This uses sccache to cache Rust compilation artifacts.
	RustSCCache *SCCache `json:"rustsccache,omitempty" yaml:"rustsccache,omitempty" jsonschema:"oneof_required=rustsccache"`
	// Bazel specifies a cache for bazel builds.
	Bazel *BazelCache `json:"bazel,omitempty" yaml:"bazel,omitempty" jsonschema:"oneof_required=bazel-local"`
}

type CacheInfo struct {
	DirInfo     CacheDirInfo
	GoBuild     GoBuildCacheInfo
	RustSCCache SCCacheInfo
	Bazel       BazelCacheInfo
}

type CacheDirInfo struct {
	// Platform sets the platform used to generate part of the cache key when
	// CacheDir.NoAutoNamespace is set to false.
	Platform *ocispecs.Platform
}

type CacheConfigOption interface {
	SetCacheConfigOption(*CacheInfo)
}

type CacheConfigOptionFunc func(*CacheInfo)

func (f CacheConfigOptionFunc) SetCacheConfigOption(info *CacheInfo) {
	f(info)
}

type CacheDirOption interface {
	SetCacheDirOption(*CacheDirInfo)
}

type CacheDirOptionFunc func(*CacheDirInfo)

func (f CacheDirOptionFunc) SetCacheDirOption(info *CacheDirInfo) {
	f(info)
}

func WithCacheDirConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.DirInfo.Platform = c.Platform
	})
}

func (c *CacheConfig) ToRunOption(worker llb.State, distroKey string, opts ...CacheConfigOption) llb.RunOption {
	if c.Dir != nil {
		return c.Dir.ToRunOption(distroKey, CacheDirOptionFunc(func(info *CacheDirInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.DirInfo
		}))
	}

	if c.GoBuild != nil {
		return c.GoBuild.ToRunOption(distroKey, GoBuildCacheOptionFunc(func(info *GoBuildCacheInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.GoBuild
		}))
	}

	if c.RustSCCache != nil {
		return c.RustSCCache.ToRunOption(distroKey, SCCacheOptionFunc(func(info *SCCacheInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.RustSCCache
		}))
	}

	if c.Bazel != nil {
		return c.Bazel.ToRunOption(worker, distroKey, BazelCacheOptionFunc(func(info *BazelCacheInfo) {
			var cacheInfo CacheInfo
			for _, opt := range opts {
				opt.SetCacheConfigOption(&cacheInfo)
			}
			*info = cacheInfo.Bazel
		}))
	}

	// Should not reach this point
	panic("invalid cache config")
}

func (c *CacheConfig) validate() error {
	if c == nil {
		return nil
	}

	var count int
	if c.Dir != nil {
		count++
	}
	if c.GoBuild != nil {
		count++
	}
	if c.RustSCCache != nil {
		count++
	}
	if c.Bazel != nil {
		count++
	}

	if count != 1 {
		return fmt.Errorf("invalid cache config: exactly one of (dir, gobuild, rustsccache, bazel) must be set")
	}

	var errs []error
	if c.Dir != nil {
		if err := c.Dir.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid cache dir config: %w", err))
		}
	}
	if c.GoBuild != nil {
		if err := c.GoBuild.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid go build cache config: %w", err))
		}
	}
	if c.RustSCCache != nil {
		if err := c.RustSCCache.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid rust sccache config: %w", err))
		}
	}
	if c.Bazel != nil {
		if err := c.Bazel.validate(); err != nil {
			errs = append(errs, fmt.Errorf("invalid bazel cache config: %w", err))
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// CacheDir is a generic cache directory configuration.
type CacheDir struct {
	// Key is the cache key to use.
	// If not set then the dest will be used.
	Key string `json:"key" yaml:"key"`
	// Dest is the directory to mount the cache to.
	Dest string `json:"dest" yaml:"dest" jsonschema:"required"`
	// Sharing is the sharing mode of the cache.
	// It can be one of the following:
	// - shared: multiple jobs can use the cache at the same time.
	// - locked: exclusive access to the cache is required.
	// - private: changes to the cache are not shared with other jobs and are discarded
	//   after the job is finished.
	Sharing string `json:"sharing" yaml:"sharing" jsonschema:"enum=shared,enum=locked,enum=private"`

	// NoAutoNamespace disables the automatic prefixing of the cache key with the
	// target specific information such as distro and CPU architecture, which may
	// be auto-injected to prevent common issues that would cause an invalid cache.
	NoAutoNamespace bool `json:"no_auto_namespace" yaml:"no_auto_namespace"`
}

func (c *CacheDir) ToRunOption(distroKey string, opts ...CacheDirOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		var sharing llb.CacheMountSharingMode
		switch c.Sharing {
		case cacheMountShared, cacheMountUnset:
			sharing = llb.CacheMountShared
		case cacheMountLocked:
			sharing = llb.CacheMountLocked
		case cacheMountPrivate:
			sharing = llb.CacheMountPrivate
		default:
			// validation needs to happen before this point
			// if we got here then this is a bug
			panic("invalid cache sharing mode")
		}

		key := c.Key
		if key == "" {
			// No key is set, so use the destination as the key.
			key = c.Dest
		}

		var info CacheDirInfo
		for _, opt := range opts {
			opt.SetCacheDirOption(&info)
		}

		if !c.NoAutoNamespace {
			platform := ei.Platform

			if platform == nil {
				platform = info.Platform
			}

			if platform == nil {
				p := platforms.DefaultSpec()
				platform = &p
			}
			key = fmt.Sprintf("%s-%s-%s", distroKey, platforms.Format(*platform), key)
		}

		llb.AddMount(c.Dest, llb.Scratch(), llb.AsPersistentCacheDir(key, sharing)).SetRunOption(ei)
	})
}

func (c *CacheDir) validate() error {
	var errs []error

	if c.Dest == "" {
		errs = append(errs, fmt.Errorf("cache dir dest is required"))
	}

	if !filepath.IsAbs(c.Dest) {
		errs = append(errs, fmt.Errorf("cache dir dest must be an absolute path: %s", c.Dest))
	}

	switch c.Sharing {
	case cacheMountShared, cacheMountLocked, cacheMountPrivate, cacheMountUnset:
	default:
		errs = append(errs, fmt.Errorf("invalid cache dir sharing mode: %s, valid values: %v", c.Sharing, []string{cacheMountShared, cacheMountLocked, cacheMountPrivate}))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// GoBuildCache is a cache for Go build artifacts.
// It is used to speed up Go builds by caching the incremental builds.
type GoBuildCache struct {
	// Scope adds extra information to the cache key.
	// This is useful to differentiate between different build contexts if required.
	//
	// This is mainly intended for internal testing purposes.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`

	// The gobuild cache may be automatically injected into a build if
	// go is detected.
	// Disabled explicitly turns this off.
	Disabled bool `json:"disabled,omitempty" yaml:"disabled,omitempty"`
}

func (c *GoBuildCache) validate() error {
	return nil
}

type GoBuildCacheInfo struct {
	Platform *ocispecs.Platform
}

type GoBuildCacheOption interface {
	SetGoBuildCacheOption(*GoBuildCacheInfo)
}

type GoBuildCacheOptionFunc func(*GoBuildCacheInfo)

func (f GoBuildCacheOptionFunc) SetGoBuildCacheOption(info *GoBuildCacheInfo) {
	f(info)
}

func WithGoCacheConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.GoBuild.Platform = c.Platform
	})
}

const goBuildCacheDir = "/tmp/dalec/gobuild-cache"

func (c *GoBuildCache) ToRunOption(distroKey string, opts ...GoBuildCacheOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		if c.Disabled {
			return
		}

		var info GoBuildCacheInfo
		for _, opt := range opts {
			opt.SetGoBuildCacheOption(&info)
		}

		platform := ei.Platform

		if platform == nil {
			platform = info.Platform
		}
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		key := fmt.Sprintf("%s-%s-dalec-gobuildcache", distroKey, platforms.Format(*platform))
		if c.Scope != "" {
			key = fmt.Sprintf("%s-%s", key, c.Scope)
		}
		llb.AddMount(goBuildCacheDir, llb.Scratch(), llb.AsPersistentCacheDir(key, llb.CacheMountShared)).SetRunOption(ei)
		llb.AddEnv("GOCACHE", goBuildCacheDir).SetRunOption(ei)
	})
}

// SCCache is a cache for Rust build artifacts.
// It uses sccache to speed up Rust compilation by caching build artifacts.
//
// NOTE: This cache downloads a pre-compiled binary from GitHub. Users must
// explicitly include this cache in their configuration to use it, which serves
// as acknowledgment of the external dependency.
//
// Future enhancement: Add support for providing sccache via build context instead of
// downloading from GitHub. This would add Source and BinaryPath fields to allow users
// to include their own verified sccache binary in the build context.
type SCCache struct {
	// Scope adds extra information to the cache key.
	// This is useful to differentiate between different build contexts if required.
	//
	// This is mainly intended for internal testing purposes.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`
}

func (c *SCCache) validate() error {
	return nil
}

type SCCacheInfo struct {
	Platform *ocispecs.Platform
}

type SCCacheOption interface {
	SetSCCacheOption(*SCCacheInfo)
}

type SCCacheOptionFunc func(*SCCacheInfo)

func (f SCCacheOptionFunc) SetSCCacheOption(info *SCCacheInfo) {
	f(info)
}

const (
	sccacheCacheDir = "/tmp/dalec/sccache-cache"
	sccacheBinary   = "/tmp/internal/dalec/sccache/sccache"
)

func (c *SCCache) ToRunOption(distroKey string, opts ...SCCacheOption) llb.RunOption {
	// TODO: Future improvement - allow pulling sccache from build context instead of GitHub
	// This would provide better security and flexibility by allowing users to:
	// 1. Bring their own verified sccache binary
	// 2. Use different versions than the hardcoded v0.10.0
	// 3. Work in offline environments
	// 4. Avoid external downloads during build time
	//
	// Implementation would add Source and BinaryPath fields to SCCache struct
	// to allow specifying a context source like: { "context": { "name": "." }, "path": "tools/sccache" }
	return RunOptFunc(func(ei *llb.ExecInfo) {
		var info SCCacheInfo
		for _, opt := range opts {
			opt.SetSCCacheOption(&info)
		}

		// Ensure platform is set
		var platform *ocispecs.Platform
		if ei.Platform != nil {
			platform = ei.Platform
		} else if info.Platform != nil {
			platform = info.Platform
		} else {
			p := platforms.DefaultSpec()
			platform = &p
		}

		key := fmt.Sprintf("%s-%s-dalec-rustsccache", distroKey, platforms.Format(*platform))
		if c.Scope != "" {
			key = fmt.Sprintf("%s-%s", key, c.Scope)
		}

		// Set up cache mount for sccache compilation cache
		llb.AddMount(sccacheCacheDir, llb.Scratch(), llb.AsPersistentCacheDir(key, llb.CacheMountShared)).SetRunOption(ei)

		// Set up environment variables
		llb.AddEnv("SCCACHE_DIR", sccacheCacheDir).SetRunOption(ei)

		// Always download and set up precompiled sccache for consistent behavior
		sccacheSource := getSccacheSource(platform)

		// Use HTTP download directly since we're in a RunOption context without SourceOpts
		sccacheDownload := llb.HTTP(sccacheSource.HTTP.URL, llb.Filename("sccache.tar.gz"))

		// Extract sccache binary using LLB state operations
		extractedSccache := ei.State.Run(
			llb.AddMount("/tmp/internal/dalec/sccache-download", sccacheDownload, llb.Readonly),
			ShArgs(`set -e
# Create temporary extraction directory
mkdir -p /tmp/internal/dalec/sccache-extract
# Extract sccache from tar.gz - the archive contains a versioned directory like sccache-v0.10.0-x86_64-unknown-linux-musl/
tar -xzf /tmp/internal/dalec/sccache-download/sccache.tar.gz -C /tmp/internal/dalec/sccache-extract/
# Find the sccache binary and copy it to output
sccache_bin=$(find /tmp/internal/dalec/sccache-extract/ -name "sccache" -type f | head -1)
if [ -n "$sccache_bin" ]; then
	cp "$sccache_bin" /output/sccache && chmod +x /output/sccache
	echo "sccache binary extracted successfully"
else
	echo "Warning: sccache binary not found in archive" >&2
	exit 1
fi`),
		).AddMount("/output", llb.Scratch())

		// Mount the extracted sccache binary to a temp directory
		llb.AddMount(sccacheBinary, extractedSccache, llb.SourcePath("sccache")).SetRunOption(ei)

		// Set up RUSTC_WRAPPER to point at the absolute sccache binary path (no PATH update needed)
		llb.AddEnv("RUSTC_WRAPPER", sccacheBinary).SetRunOption(ei)
	})
} // BazelCache sets up a cache for bazel builds.
// Currently this only supports setting up a *local* bazel cache.
//
// BazelCache relies on the *system* bazelrc file to configure the default cache location.
// If the project being built includes its own bazelrc it may override the one configured by BazelCache.
//
// An alternative to BazelCache would be a [CacheDir] and use `--disk_cache` to set the cache location
// when executing bazel commands.
type BazelCache struct {
	// Scope adds extra information to the cache key.
	// This is useful to differentiate between different build contexts if required.
	//
	// This is mainly intended for internal testing purposes.
	Scope string `json:"scope,omitempty" yaml:"scope,omitempty"`
}

func (c *BazelCache) validate() error {
	return nil
}

type BazelCacheInfo struct {
	Platform    *ocispecs.Platform
	constraints *llb.Constraints
}

func WithBazelCacheConstraints(opts ...llb.ConstraintsOpt) CacheConfigOption {
	return CacheConfigOptionFunc(func(info *CacheInfo) {
		var c llb.Constraints
		for _, opt := range opts {
			opt.SetConstraintsOption(&c)
		}
		info.Bazel.Platform = c.Platform
		info.Bazel.constraints = &c
	})
}

type BazelCacheOptionFunc func(*BazelCacheInfo)

func (f BazelCacheOptionFunc) SetBazelCacheOption(info *BazelCacheInfo) {
	f(info)
}

type BazelCacheOption interface {
	SetBazelCacheOption(*BazelCacheInfo)
}

func (c *BazelCache) ToRunOption(worker llb.State, distroKey string, opts ...BazelCacheOption) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		var info BazelCacheInfo

		for _, opt := range opts {
			opt.SetBazelCacheOption(&info)
		}

		platform := ei.Platform

		if platform == nil {
			platform = info.Platform
		}
		if platform == nil {
			p := platforms.DefaultSpec()
			platform = &p
		}

		key := fmt.Sprintf("%s-%s-dalec-bazelcache", distroKey, platforms.Format(*platform))
		if c.Scope != "" {
			key = fmt.Sprintf("%s-%s", key, c.Scope)
		}

		// See bazelrc https://bazel.build/run/bazelrc for more information on the bazelrc file

		const (
			cacheDir = "/tmp/dalec/bazel-local-cache"
			sockPath = "/tmp/dalec/bazel-remote.sock"
		)

		rcFileContent := `
build --disk_cache=` + cacheDir + `
fetch --disk_cache=` + cacheDir + `
`
		rcFile := llb.Scratch().File(
			llb.Mkfile("bazelrc", 0o644, []byte(rcFileContent)),
			WithConstraint(info.constraints),
		)

		checkSockPath := filepath.Join(filepath.Dir(sockPath), c.Scope, filepath.Base(sockPath))
		checkScript := fmt.Sprintf(`#!/usr/bin/env sh

if [ -S %q ]; then
	echo "build --remote_cache=unix:%s" >> /tmp/dalec/bazelrc
	echo "fetch --remote_cache=unix:%s" >> /tmp/dalec/bazelrc
fi
`, checkSockPath, sockPath, sockPath)
		checkScriptSt := llb.Scratch().File(
			llb.Mkfile("script.sh", 0o755, []byte(checkScript)),
			WithConstraint(info.constraints),
		)

		scriptPath := "/tmp/dalec/internal/bazel/check-socket.sh"
		rcFile = worker.Run(
			llb.AddSSHSocket(llb.SSHID(BazelDefaultSocketID), llb.SSHSocketTarget(checkSockPath), llb.SSHOptional),
			ShArgs(scriptPath),
			llb.AddMount(scriptPath, checkScriptSt, llb.SourcePath("script.sh")),
			WithConstraint(info.constraints),
		).AddMount("/tmp/dalec/bazelrc", rcFile, llb.SourcePath("bazelrc"))

		llb.AddMount("/etc/bazel.bazelrc", rcFile, llb.SourcePath("bazelrc")).SetRunOption(ei)
		llb.AddMount(cacheDir, llb.Scratch(), llb.AsPersistentCacheDir(key, llb.CacheMountShared)).SetRunOption(ei)
		llb.AddSSHSocket(llb.SSHID(BazelDefaultSocketID), llb.SSHSocketTarget(sockPath), llb.SSHOptional).SetRunOption(ei)
	})
}
