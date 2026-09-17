package frontend

import (
	"context"
	"maps"
	"testing"

	"github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestImageConfigOpts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		inputs  map[string]string
		enabled bool
		invalid bool
	}{
		{name: "absent"},
		{name: "enabled", inputs: map[string]string{KeyImageSourceLabel: "true"}, enabled: true},
		{name: "disabled", inputs: map[string]string{KeyImageSourceLabel: "false"}},
		{name: "numeric true", inputs: map[string]string{KeyImageSourceLabel: "1"}, enabled: true},
		{name: "numeric false", inputs: map[string]string{KeyImageSourceLabel: "0"}},
		{name: "empty", inputs: map[string]string{KeyImageSourceLabel: ""}, invalid: true},
		{name: "invalid", inputs: map[string]string{KeyImageSourceLabel: "yes"}, invalid: true},
		{name: "build arg cannot enable", inputs: map[string]string{"build-arg:" + KeyImageSourceLabel: "true"}},
		{name: "input overrides build arg", inputs: map[string]string{KeyImageSourceLabel: "false", "build-arg:" + KeyImageSourceLabel: "true"}},
		{name: "build arg cannot disable input", inputs: map[string]string{KeyImageSourceLabel: "true", "build-arg:" + KeyImageSourceLabel: "false"}, enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newStubClient()
			maps.Copy(c.opts, tc.inputs)
			opts, err := ImageConfigOpts(c)
			if tc.invalid {
				assert.ErrorContains(t, err, "invalid "+KeyImageSourceLabel)
				return
			}
			assert.NilError(t, err)
			spec := &dalec.Spec{Sources: map[string]dalec.Source{
				"src": {Git: &dalec.SourceGit{URL: "https://github.com/coredns/coredns.git"}},
			}}
			img := &dalec.DockerImageSpec{}
			img.Config.Labels = map[string]string{
				ocispecs.AnnotationSource:   "https://example.com/base",
				ocispecs.AnnotationRevision: "base-commit",
				"org.label-schema.vcs-url":  "https://example.com/legacy-base",
				"org.label-schema.vcs-ref":  "legacy-base-commit",
			}
			want := maps.Clone(img.Config.Labels)
			if tc.enabled {
				want = map[string]string{ocispecs.AnnotationSource: "https://github.com/coredns/coredns.git"}
			}
			assert.NilError(t, dalec.BuildImageConfig(spec, "", img, opts...))
			assert.DeepEqual(t, img.Config.Labels, want)
		})
	}
}

func TestImageSourceLabelInputForwarding(t *testing.T) {
	c := newStubClient()
	c.opts[KeyImageSourceLabel] = "true"
	req := &client.SolveRequest{}
	assert.NilError(t, copyForForward(context.Background(), c)(req))
	assert.Equal(t, req.FrontendOpt[KeyImageSourceLabel], "true")
	_, ok := req.FrontendOpt["build-arg:"+KeyImageSourceLabel]
	assert.Assert(t, !ok)
}
