package debug

import (
	"context"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec/frontend"
)

const DebugRoute = "debug"

func Handle(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
	var r frontend.BuildMux

	r.Add("resolve", Resolve, &targets.Target{
		Name:        "resolve",
		Description: "Outputs the resolved dalec spec file with build args applied.",
	})
	r.Add("sources", Sources, &targets.Target{
		Name:        "sources",
		Description: "Outputs all sources from a dalec spec file.",
	})
	r.Add("patched-sources", PatchedSources, &targets.Target{
		Name:        "patched-sources",
		Description: "Outputs all patched sources from a dalec spec file.",
	})
	r.Add("gomods", Gomods, &targets.Target{
		Name:        "gomods",
		Description: "Outputs all the gomodule dependencies for the spec",
	})
	r.Add("cargohome", Cargohome, &targets.Target{
		Name:        "cargohome",
		Description: "Outputs all the Cargo dependencies for the spec",
	})
	r.Add("pip", Pip, &targets.Target{
		Name:        "pip",
		Description: "Outputs all the pip dependencies for the spec",
	})

	return r.Handle(ctx, client)
}
