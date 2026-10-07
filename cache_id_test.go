package dalec

import (
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
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
