package frontend

import (
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestNamedImageRoutes(t *testing.T) {
	spec := &dalec.Spec{Targets: map[string]dalec.Target{
		"distro": {Images: map[string]dalec.NamedImage{"minimal": {}, "full": {}}},
	}}
	var selected string
	routes, err := AppendNamedImageRoutes(nil, "distro", spec, func(name string) gwclient.BuildFunc {
		return stubHandler(func() { selected = name })
	})
	assert.NilError(t, err)
	var router Router
	for _, route := range routes {
		router.Add(t.Context(), route)
		assert.Assert(t, route.Info.SpecDefined)
		assert.Assert(t, !route.Info.Default && !route.Info.Hidden)
	}
	_, err = router.Handle(t.Context(), newStubClient(withStubOptTarget("distro/container/minimal")))
	assert.NilError(t, err)
	assert.Equal(t, selected, "minimal")
	_, err = router.Handle(t.Context(), newStubClient(withStubOptTarget("distro/container/missing")))
	assert.Assert(t, err != nil)
	_, err = AppendNamedImageRoutes(routes, "distro", spec, func(string) gwclient.BuildFunc { return nil })
	assert.ErrorContains(t, err, "conflicts with built-in target")
}
