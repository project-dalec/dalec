package dalec

import (
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
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
