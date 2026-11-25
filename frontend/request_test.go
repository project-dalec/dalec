package frontend

import (
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

func TestSourceFilterConfig(t *testing.T) {
	t.Parallel()

	filterArgs := []struct {
		name        string
		args        []stubOpt
		wantContext string
	}{
		{
			name:        "a config path build arg",
			args:        []stubOpt{withStubBuildArg(dalec.BuildArgDalecSourceFilterConfigPath, "custom-filter.yml")},
			wantContext: dalec.DefaultSourceOptionsContextName,
		},
		{
			name:        "a context name build arg",
			args:        []stubOpt{withStubBuildArg(dalec.BuildArgDalecSourceFilterContextName, "other-context")},
			wantContext: "other-context",
		},
		{
			name: "both filter build args",
			args: []stubOpt{
				withStubBuildArg(dalec.BuildArgDalecSourceFilterConfigPath, "custom-filter.yml"),
				withStubBuildArg(dalec.BuildArgDalecSourceFilterContextName, "other-context"),
			},
			wantContext: "other-context",
		},
	}

	// A context that is not part of the build resolves to nil. The stub client
	// cannot solve, so reading a config at all fails the test.
	var requestedContext string
	getContext := func(name string, _ ...llb.LocalOption) (*llb.State, error) {
		requestedContext = name
		return nil, nil
	}

	t.Run("a build without the source options context is not filtered", func(t *testing.T) {
		cfg, err := loadSourceFilterConfig(t.Context(), newStubClient(), getContext)
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.IsEmpty() {
			t.Fatalf("expected no filtering, got %v", cfg.GlobalExcludes)
		}
		if requestedContext != dalec.DefaultSourceOptionsContextName {
			t.Errorf("expected build context %q to be looked up, got %q", dalec.DefaultSourceOptionsContextName, requestedContext)
		}
	})

	for _, tc := range filterArgs {
		t.Run(tc.name+" without the source options context fails the build", func(t *testing.T) {
			_, err := loadSourceFilterConfig(t.Context(), newStubClient(tc.args...), getContext)
			if err == nil {
				t.Fatal("expected an error when the build asks for a filter config the context cannot provide")
			}
			if requestedContext != tc.wantContext {
				t.Errorf("expected build context %q to be looked up, got %q", tc.wantContext, requestedContext)
			}
			if !strings.Contains(err.Error(), tc.wantContext) {
				t.Errorf("expected error to name build context %q, got %v", tc.wantContext, err)
			}
		})
	}
}

func TestSourceFilterConfigUsesContextRelativePath(t *testing.T) {
	t.Parallel()

	var gotOpts []llb.LocalOption
	getContext := func(_ string, opts ...llb.LocalOption) (*llb.State, error) {
		gotOpts = opts
		return nil, nil
	}

	client := newStubClient(withStubBuildArg(dalec.BuildArgDalecSourceFilterConfigPath, "/./source-filter.yml"))
	if _, err := loadSourceFilterConfig(t.Context(), client, getContext); err == nil {
		t.Fatal("expected an error because the source options context is missing")
	}

	var li llb.LocalInfo
	for _, o := range gotOpts {
		o.SetLocalOption(&li)
	}

	const want = `["source-filter.yml"]`
	if li.IncludePatterns != want {
		t.Errorf("expected include patterns %s, got %s", want, li.IncludePatterns)
	}
	if li.FollowPaths != want {
		t.Errorf("expected follow paths %s, got %s", want, li.FollowPaths)
	}
}
