package dalec

import (
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec/internal/test"
	internaltest "github.com/project-dalec/dalec/internal/test"
)

func TestPersistentCacheIDString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   PersistentCacheID
		want string
	}{
		{
			name: "all parts",
			id: PersistentCacheID{
				Namespace:   "tenant",
				Environment: "ubuntu22.04",
				Platform:    "linux/amd64",
				Type:        "dalec-gobuildcache",
				Key:         "scope",
			},
			want: "tenant/ubuntu22.04-linux/amd64-dalec-gobuildcache-scope",
		},
		{
			name: "empty parts omitted",
			id: PersistentCacheID{
				Environment: "azlinux3.0",
				Type:        "dalec-bazelcache",
			},
			want: "azlinux3.0-dalec-bazelcache",
		},
		{
			name: "trailing namespace slash trimmed",
			id: PersistentCacheID{
				Namespace: "ci/",
				Type:      "dalec-gomod-proxy-cache",
			},
			want: "ci/dalec-gomod-proxy-cache",
		},
		{
			name: "user key preserved",
			id: PersistentCacheID{
				Key: "/tmp/cache",
			},
			want: "/tmp/cache",
		},
		{
			name: "namespace with user key only",
			id: PersistentCacheID{
				Namespace: "ci",
				Key:       "user-cache",
			},
			want: "ci/user-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.id.String(); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestFormatSafeCacheIDPlatform(t *testing.T) {
	t.Parallel()

	p := ocispecs.Platform{
		OS:           "linux",
		Architecture: "arm64",
	}

	if got, want := FormatSafeCacheIDPlatform(p), "linux_arm64"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestCacheDirAutoNamespaceIncludesGenericCacheType(t *testing.T) {
	t.Parallel()

	st := llb.Scratch().Run(
		ShArgs("true"),
		(&CacheDir{Dest: "/tmp/cache", Key: "dalec-gobuildcache"}).ToRunOption("azlinux3.0"),
	).Root()

	for _, op := range internaltest.LLBOpsFromState(t.Context(), t, st) {
		exec := op.Op.GetExec()
		if exec == nil {
			continue
		}
		for _, mount := range exec.Mounts {
			if mount.CacheOpt != nil && strings.Contains(mount.CacheOpt.ID, "-"+cacheTypeGeneric+"-") {
				return
			}
		}
	}

	t.Fatalf("expected generic cache type %q in auto-namespaced cache ID", cacheTypeGeneric)
}

func TestBuildkitCacheMountNamespaceIsKnownBuildArg(t *testing.T) {
	t.Parallel()

	if !knownArg(BuildArgBuildkitCacheMountNS) {
		t.Fatalf("expected %s to be a known build arg", BuildArgBuildkitCacheMountNS)
	}
}

func TestCacheMountNamespaceAppliedToCacheConfig(t *testing.T) {
	t.Parallel()

	cache := CacheConfig{
		GoBuild: &GoBuildCache{
			Scope: "scope",
		},
	}
	platform := ocispecs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}

	st := llb.Scratch().Run(
		ShArgs("true"),
		cache.ToRunOption(
			llb.Scratch(),
			"azlinux3.0",
			WithCacheNamespace("ci"),
			WithGoCacheConstraints(llb.Platform(platform)),
		),
	).Root()

	assertCacheMountIDs(t, st, "ci/azlinux3.0-linux/amd64-dalec-gobuildcache-scope")
}

func TestCacheMountNamespaceAppliedToCacheDir(t *testing.T) {
	t.Parallel()

	cache := CacheConfig{
		Dir: &CacheDir{
			Key:  "user-cache",
			Dest: "/tmp/cache",
		},
	}
	platform := ocispecs.Platform{
		OS:           "linux",
		Architecture: "amd64",
	}

	st := llb.Scratch().Run(
		ShArgs("true"),
		cache.ToRunOption(
			llb.Scratch(),
			"azlinux3.0",
			WithCacheNamespace("ci"),
			WithCacheDirConstraints(llb.Platform(platform)),
		),
	).Root()

	assertCacheMountIDs(t, st, "ci/azlinux3.0-linux/amd64-dalec-genericcache-user-cache")
}

func TestCacheMountNamespaceAppliedToCacheDirWithoutAutoNamespace(t *testing.T) {
	t.Parallel()

	cache := CacheConfig{
		Dir: &CacheDir{
			Key:             "user-cache",
			Dest:            "/tmp/cache",
			NoAutoNamespace: true,
		},
	}

	st := llb.Scratch().Run(
		ShArgs("true"),
		cache.ToRunOption(
			llb.Scratch(),
			"azlinux3.0",
			WithCacheNamespace("ci"),
		),
	).Root()

	assertCacheMountIDs(t, st, "ci/user-cache")
}

func TestCacheMountNamespaceAppliedToAptCache(t *testing.T) {
	t.Parallel()

	st := llb.Scratch().Run(
		ShArgs("true"),
		WithMountedAptCacheNamespace("jammy", "ci"),
	).Root()

	assertCacheMountIDs(t, st,
		"ci/jammy-dalec-var-cache-apt",
		"ci/jammy-dalec-var-lib-apt",
	)
}

func TestAptCacheMountUsesCentralizedIDWithoutNamespace(t *testing.T) {
	t.Parallel()

	st := llb.Scratch().Run(
		ShArgs("true"),
		WithMountedAptCache("jammy"),
	).Root()

	assertCacheMountIDs(t, st,
		"jammy-dalec-var-cache-apt",
		"jammy-dalec-var-lib-apt",
	)
}

func assertCacheMountIDs(t *testing.T, st llb.State, want ...string) {
	t.Helper()

	got := map[string]struct{}{}
	for _, op := range test.LLBOpsFromState(t.Context(), t, st) {
		exec := op.Op.GetExec()
		if exec == nil {
			continue
		}
		for _, mount := range exec.Mounts {
			if mount.CacheOpt == nil {
				continue
			}
			got[mount.CacheOpt.ID] = struct{}{}
		}
	}

	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Fatalf("expected cache mount ID %q, got %v", id, got)
		}
	}
}
