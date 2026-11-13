package frontend

import (
	"testing"

	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/project-dalec/dalec"
)

func TestSourceOptFromUIClientReadsGomodProxyBuildArgIntoExtraEnvs(t *testing.T) {
	t.Parallel()

	const proxy = "http://proxy.example:5000,direct"
	client := newStubClient()
	client.opts["build-arg:"+dalec.BuildArgDalecGomodProxy] = proxy

	sOpt := SourceOptFromUIClient(t.Context(), client, nil, nil)
	if sOpt.ExtraEnvs["GOPROXY"] != proxy {
		t.Fatalf("expected GOPROXY extra env %q, got %q", proxy, sOpt.ExtraEnvs["GOPROXY"])
	}
}

func TestSourceOptFromUIClientCopiesCargoBuildArgs(t *testing.T) {
	t.Parallel()

	client := newStubClient()
	dc := &dockerui.Client{Config: dockerui.Config{BuildArgs: map[string]string{
		dalec.BuildArgDalecCargoRegistry:     "https://pkgs.example.test/feed",
		dalec.BuildArgDalecCargoRegistryName: "test-feed",
	}}}

	sOpt := SourceOptFromUIClient(t.Context(), client, dc, nil)
	if got := sOpt.BuildArgs[dalec.BuildArgDalecCargoRegistry]; got != "https://pkgs.example.test/feed" {
		t.Fatalf("expected Cargo registry URL build arg, got %q", got)
	}
	if got := sOpt.BuildArgs[dalec.BuildArgDalecCargoRegistryName]; got != "test-feed" {
		t.Fatalf("expected Cargo registry name build arg, got %q", got)
	}
}
