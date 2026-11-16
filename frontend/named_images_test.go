package frontend

import (
	"fmt"
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

func TestContainerRoutesRejectUnknownSuffixes(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprintf("named=%t", named), func(t *testing.T) {
			spec := &dalec.Spec{Targets: map[string]dalec.Target{"distro": {}}}
			if named {
				spec.Targets["distro"] = dalec.Target{Images: map[string]dalec.NamedImage{"minimal": {}}}
			}
			var called string
			handler := func(name string) gwclient.BuildFunc {
				return stubHandler(func() { called = name })
			}
			routes, err := AppendNamedImageRoutes([]Route{
				{FullPath: "distro", Handler: handler("alias")},
				{FullPath: "distro/container", Handler: handler("default")},
				{FullPath: "distro/container/depsonly", Handler: handler("depsonly")},
			}, "distro", spec, handler)
			assert.NilError(t, err)
			var router Router
			for _, route := range routes {
				router.Add(t.Context(), route)
			}
			for _, target := range []string{
				"distro/container/missing",
				"distro/container/minimal/typo",
				"distro/container/depsonly/typo",
			} {
				called = ""
				_, err := router.Handle(t.Context(), newStubClient(withStubOptTarget(target)))
				assert.Assert(t, err != nil, "target %s must fail", target)
				assert.Equal(t, called, "")
			}
			for target, want := range map[string]string{
				"distro":                    "alias",
				"distro/container":          "default",
				"distro/container/depsonly": "depsonly",
			} {
				_, err := router.Handle(t.Context(), newStubClient(withStubOptTarget(target)))
				assert.NilError(t, err)
				assert.Equal(t, called, want)
			}
			if named {
				_, err := router.Handle(t.Context(), newStubClient(withStubOptTarget("distro/container/minimal")))
				assert.NilError(t, err)
				assert.Equal(t, called, "minimal")
			}
		})
	}
}

func TestNamedImageRoutesPreserveForwardingPrefixes(t *testing.T) {
	called := false
	routes, err := AppendNamedImageRoutes([]Route{{
		FullPath: "distro/container",
		Handler:  stubHandler(func() { called = true }),
		Forward:  &Forward{},
	}}, "distro", &dalec.Spec{}, nil)
	assert.NilError(t, err)
	var router Router
	router.Add(t.Context(), routes[0])
	_, err = router.Handle(t.Context(), newStubClient(withStubOptTarget("distro/container/remote-image")))
	assert.NilError(t, err)
	assert.Assert(t, called)
}
