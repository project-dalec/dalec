package frontend

import (
	"testing"

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
