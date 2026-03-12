package debug

import (
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec/frontend"
)

const DebugRoute = "debug"

// Routes returns the flat routes for the debug handler, prefixed with the given prefix.
func Routes(prefix string) []frontend.Route {
	return []frontend.Route{
		{
			FullPath: prefix + "/resolve",
			Handler:  Resolve,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/resolve",
					Description: "Outputs the resolved dalec spec file with build args applied.",
				},
			},
		},
		{
			FullPath: prefix + "/sources",
			Handler:  Sources,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/sources",
					Description: "Outputs all sources from a dalec spec file.",
				},
			},
		},
		{
			FullPath: prefix + "/patched-sources",
			Handler:  PatchedSources,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/patched-sources",
					Description: "Outputs all patched sources from a dalec spec file.",
				},
			},
		},
		{
			FullPath: prefix + "/gomods",
			Handler:  Gomods,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/gomods",
					Description: "Outputs all the gomodule dependencies for the spec",
				},
			},
		},
		{
			FullPath: prefix + "/cargohome",
			Handler:  Cargohome,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/cargohome",
					Description: "Outputs all the Cargo dependencies for the spec",
				},
			},
		},
		{
			FullPath: prefix + "/pip",
			Handler:  Pip,
			Info: frontend.Target{
				Target: bktargets.Target{
					Name:        prefix + "/pip",
					Description: "Outputs all the pip dependencies for the spec",
				},
			},
		},
	}
}
