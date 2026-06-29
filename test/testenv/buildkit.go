package testenv

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/buildkit/client"
)

type buildkitVersion struct {
	Major int
	Minor int
}

func (v buildkitVersion) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

var (
	minVersion = buildkitVersion{0, 12}
)

// supportsFrontendAsInput returns true if the buildkit instance allows you to pass LLB references as inputs to a solve request.
// This would be needed when testing custom frontends separate from the main one.
//
// More info:
// Buildkit treats the frontend ref (`#syntax=<ref>` or via the BUILDKIT_SYNTAX
// var) as a docker image ref.
// Buildkit will always check the remote registry for a new version of the image.
// As of buildkit v0.12 you can use named contexts to overwrite the frontend ref
// with another type of ref.
// This can be another docker-image, an oci-layout, or even a frontend "input"
// (like feeding the output of a build into another build).
// Here we are checking the version of buildkit to determine what method we can
// use.
func supportsFrontendAsInput(info *client.Info) bool {
	majorStr, minorPatchStr, ok := strings.Cut(strings.TrimPrefix(info.BuildkitVersion.Version, "v"), ".")
	if !ok {
		return false
	}

	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return false
	}

	if major < minVersion.Major {
		return false
	}

	if major > minVersion.Major {
		return true
	}

	minorStr, _, _ := strings.Cut(minorPatchStr, ".")

	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return false
	}

	return minor >= minVersion.Minor
}

// withGHCache adds the necessary cache export and import options to the solve request in order to use the GitHub Actions cache.
// It uses the test name as a scope for the cache. Each test will have its own scope.
// This means that caches are not shared between tests, but it also means that tests won't overwrite each other's cache.
//
// Github Actions sets some specific environment variables that we'll look for to even determine if we should configure the cache or not.
//
// This is effectively what `docker build --cache-from=gha,scope=foo --cache-to=gha,scope=foo` would do.
// Export uses the default min mode: only the final layers are pushed. The heavy
// base/worker layers are cached separately via the prebuilt worker images, so
// there's no need to bloat each test scope (and the 10GB cap) with mode=max.
//
// Note: we talk to buildkit via its Go client rather than buildctl/buildx, so
// the daemon does not auto-detect these values from the environment; we must
// pass them as attrs. The token/url env vars are only present in GitHub Actions
// jobs that expose the runtime (see crazy-max/ghaction-github-runtime). We only
// target the v2 cache service; v1 was retired in March 2025.
func withGHCache(t *testing.T, so *client.SolveOpt) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		// This is not running in GitHub Actions, so we don't need to configure the cache.
		return
	}

	token := os.Getenv("ACTIONS_RUNTIME_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "::warning::ACTIONS_RUNTIME_TOKEN is not set, skipping cache export")
		return
	}

	url := os.Getenv("ACTIONS_RESULTS_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "::warning::ACTIONS_RESULTS_URL is not set, skipping cache export")
		return
	}

	scope := "test-integration-" + t.Name()

	so.CacheExports = append(so.CacheExports, client.CacheOptionsEntry{
		Type: "gha",
		Attrs: map[string]string{
			"scope":  scope,
			"token":  token,
			"url_v2": url,
		},
	})
	so.CacheImports = append(so.CacheImports, client.CacheOptionsEntry{
		Type: "gha",
		Attrs: map[string]string{
			"scope":  scope,
			"token":  token,
			"url_v2": url,
		},
	})
}
