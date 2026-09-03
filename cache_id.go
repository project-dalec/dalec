package dalec

import (
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	cacheTypeGoBuild     = "dalec-gobuildcache"
	cacheTypeRustSccache = "dalec-rustsccache"
	cacheTypeBazel       = "dalec-bazelcache"
)

// PersistentCacheID describes a Dalec persistent BuildKit cache mount ID.
type PersistentCacheID struct {
	// Namespace is an optional global namespace prepended to the whole cache ID.
	Namespace string
	// Environment identifies the build environment that owns the cache.
	Environment string
	// Platform identifies the platform when a cache must be platform-scoped.
	Platform string
	// Type identifies the Dalec cache type.
	Type string
	// Key identifies user-provided cache key material, scope, or a sub-cache.
	Key string
}

// String format the cache ID from its non-empty parts.
func (id PersistentCacheID) String() string {
	parts := make([]string, 0, 4)
	for _, part := range []string{id.Environment, id.Platform, id.Type, id.Key} {
		if part != "" {
			parts = append(parts, part)
		}
	}

	cacheID := strings.Join(parts, "-")
	if id.Namespace == "" {
		return cacheID
	}

	ns := strings.TrimRight(id.Namespace, "/")
	if cacheID == "" {
		return ns
	}
	return ns + "/" + cacheID
}

// FormatCacheIDPlatform formats a platform for use in cache IDs.
func FormatCacheIDPlatform(p ocispecs.Platform) string {
	return platforms.Format(p)
}

// FormatSafeCacheIDPlatform formats a platform without path separators.
func FormatSafeCacheIDPlatform(p ocispecs.Platform) string {
	return strings.NewReplacer("/", "_", ":", "_").Replace(FormatCacheIDPlatform(p))
}

func defaultedCacheIDPlatform(p *ocispecs.Platform) string {
	if p == nil {
		dp := platforms.DefaultSpec()
		p = &dp
	}
	return FormatCacheIDPlatform(*p)
}

func execCacheIDPlatform(ei *llb.ExecInfo, fallback *ocispecs.Platform) string {
	if ei.Platform != nil {
		return defaultedCacheIDPlatform(ei.Platform)
	}
	return defaultedCacheIDPlatform(fallback)
}
