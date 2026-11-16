package frontend

import (
	"fmt"
	"strings"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	bktargets "github.com/moby/buildkit/frontend/subrequests/targets"
	"github.com/project-dalec/dalec"
)

// AppendNamedImageRoutes registers exact container routes, preserving forwarding prefixes.
func AppendNamedImageRoutes(routes []Route, prefix string, spec *dalec.Spec, handler func(string) gwclient.BuildFunc) ([]Route, error) {
	containerPath := prefix + "/container"
	for i := range routes {
		route := &routes[i]
		if route.Forward == nil && (route.FullPath == containerPath || strings.HasPrefix(route.FullPath, containerPath+"/")) {
			route.Exact = true
		}
	}
	for name := range dalec.SortedMapIter(spec.Targets[prefix].Images) {
		fullPath := prefix + "/container/" + name
		for _, route := range routes {
			if route.FullPath == fullPath {
				return nil, fmt.Errorf("image %q conflicts with built-in target %q", name, fullPath)
			}
		}
		routes = append(routes, Route{
			FullPath: fullPath,
			Exact:    true,
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
