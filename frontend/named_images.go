package frontend

import (
	"fmt"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec"
)

// AppendNamedImageRoutes registers explicit container routes, preserving built-in routes.
func AppendNamedImageRoutes(routes []Route, prefix string, spec *dalec.Spec, handler func(string) gwclient.BuildFunc) ([]Route, error) {
	for name := range dalec.SortedMapIter(spec.Targets[prefix].Images) {
		fullPath := prefix + "/container/" + name
		for _, route := range routes {
			if route.FullPath == fullPath {
				return nil, fmt.Errorf("image %q conflicts with built-in target %q", name, fullPath)
			}
		}
		routes = append(routes, Route{
			FullPath: fullPath,
			Handler:  handler(name),
			Info: Target{
				Target: bktargets.Target{
					Name:        fullPath,
					Description: "Builds named container image " + name,
				},
				SpecDefined: true,
			},
		})
	}
	return routes, nil
}
