package test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/internal/frontendapi"
	"github.com/project-dalec/dalec/sessionutil/socketprovider"
	"github.com/project-dalec/dalec/targets"
	"github.com/project-dalec/dalec/test/internal/bazeltest"
	"github.com/project-dalec/dalec/test/testenv"
	"google.golang.org/grpc"
	"gotest.tools/v3/assert"
)

func testArtifactBuildCacheDir(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	// Add a random key to all the to make sure they are unique
	// for test runs and parallel tests don't interfere with each other.
	// We also use this key in each of the dest paths to force cache invalidation.
	// This is important because we need each case to actually run uncached (at least from the actual build part)
	// otherwise the test will almost certainly fail if the part that writes data is cached but the part that reads it is not.
	randKey := getRand()

	caches := []dalec.CacheConfig{
		{
			Dir: &dalec.CacheDir{
				Dest: filepath.Join("/tmp/cache", randKey+"1"),
			},
		},
		{
			Dir: &dalec.CacheDir{
				Key:  randKey,
				Dest: filepath.Join("/tmp/cache", randKey+"2"),
			},
		},
		{
			Dir: &dalec.CacheDir{
				Key:             randKey,
				Dest:            filepath.Join("/tmp/cache", randKey+"3"),
				NoAutoNamespace: true,
			},
		},
		{
			GoBuild: &dalec.GoBuildCache{
				Scope: randKey,
			},
		},
		{
			RustSCCache: &dalec.SCCache{
				Scope: randKey,
			},
		},
		{
			Bazel: &dalec.BazelCache{
				Scope: randKey,
			},
		},
	}

	specWithCommand := func(cmds ...string) *dalec.Spec {
		cmds = append([]string{"set -ex;"}, cmds...)
		spec := newSimpleSpec()
		spec.Build.Caches = caches
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: strings.Join(cmds, "\n"),
		})

		return spec
	}

	getDir := func(t *testing.T, c dalec.CacheConfig) string {
		if c.Dir != nil {
			return c.Dir.Dest
		}
		if c.GoBuild != nil {
			return "${GOCACHE}"
		}
		if c.RustSCCache != nil {
			return "${SCCACHE_DIR}"
		}
		if c.Bazel != nil {
			// There is no good way to determine the bazel cache dir
			// So just hardcode this for now
			return "/tmp/dalec/bazel-local-cache"
		}
		t.Fatalf("invalid cache config or maybe the test needs to be updated for a new cache type?")
		return ""
	}

	distro, _, _ := strings.Cut(cfg.Package, "/")

	cmds := make([]string, 0, len(caches))

	// Makes sure the cache is populated with some data.
	populateCache := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		for i, c := range caches {
			dir := getDir(t, c)
			cmds = append(cmds, fmt.Sprintf("echo %s %d > \"%s/hello\"", distro, i, dir))

			if c.Bazel != nil {
				cmds = append(cmds, fmt.Sprintf("grep %q /etc/bazel.bazelrc && exit; cat /etc/bazel.bazelrc; exit 42", dir))
			}
		}

		spec := specWithCommand(cmds...)
		// Ignore the pkg buildkit cache for the pkg build to ensure our commands always run
		// Otherwise we can wind up in a case where this request is fully cached but there is nothing in the cache dirs.
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
		solveT(ctx, t, client, sr)
	}

	// Checks that a cache is pre-populated from anothwer build.
	checkCacheContents := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		for i, c := range caches {
			check := fmt.Sprintf("%s %d", distro, i)
			cmds = append(cmds, fmt.Sprintf("grep %q %s/hello\n", check, getDir(t, c)))
		}

		spec := specWithCommand(cmds...)
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)
	}

	checkDistro := func(ctx context.Context, t *testing.T, client gwclient.Client) {
		// Used to validate that caches with namespaced dirs are not shared between distros and that
		// caches with no namespace are shared between distros.
		altDistro := frontendapi.TestingAltTargetKey(distro)
		altTarget := strings.ReplaceAll(cfg.Package, distro, altDistro)
		// Note: cache3/hello3 should have the content written by the first test
		for i, c := range caches {
			dir := getDir(t, c)

			if c.Dir != nil && !c.Dir.NoAutoNamespace {
				cmds = append(cmds, fmt.Sprintf("[ -d %s ]; [ ! -f %s ]", dir, filepath.Join(dir, "hello")))
				continue
			}

			switch {
			case c.Dir != nil:
				// Use the *original* distro name here since that is what wrote the file
				check := fmt.Sprintf("%s %d", distro, i)
				cmds = append(cmds, fmt.Sprintf("grep %q %s", check, filepath.Join(dir, "hello")))
			case c.GoBuild != nil || c.RustSCCache != nil || c.Bazel != nil:
				// This should not exist because the cache is not shared between distros
				cmds = append(cmds, fmt.Sprintf("[ ! -f %q ]", filepath.Join(dir, "hello")))
			}
		}

		spec := specWithCommand(cmds...)
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(altTarget))
		solveT(ctx, t, client, sr)
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		// Make sure the cache is populated
		populateCache(ctx, t, client)

		// Now make sure the cache is persistent by running another build and checking the cache contents
		cmds = cmds[:0]
		checkCacheContents(ctx, t, client)

		// Now make sure the cache is not shared with another distro
		// This makes sure that that the 1st and 2nd cache are not shared with another distro, but the 3rd one is.
		cmds = cmds[:0]
		checkDistro(ctx, t, client)
	})
}

func getRand() string {
	buf := make([]byte, 16)
	n, _ := rand.Read(buf)
	return hex.EncodeToString(buf[:n])
}

func testBazelCache(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	bzlPkg := cfg.GetPackage("bazel")
	if bzlPkg == noPackageAvailable {
		t.Skip("bazel not available in this distro")
	}

	// Add a random key to all the to make sure they are unique
	// for test runs and parallel tests don't interfere with each other.
	// We also use this key in each of the dest paths to force cache invalidation.
	// This is important because we need each case to actually run uncached (at least from the actual build part)
	// otherwise the test will almost certainly fail if the part that writes data is cached but the part that reads it is not.

	newSpec := func(randKey string, cmds ...string) *dalec.Spec {
		spec := newSimpleSpec()
		spec.Build.Caches = []dalec.CacheConfig{
			{
				Bazel: &dalec.BazelCache{
					Scope: randKey,
				},
			},
		}

		spec.Dependencies = &dalec.PackageDependencies{}
		spec.Dependencies.Build = map[string]dalec.PackageConstraints{
			bzlPkg: {},
		}
		spec.Sources["src"] = dalec.Source{
			Inline: &dalec.SourceInline{
				Dir: &dalec.SourceInlineDir{
					Files: map[string]*dalec.SourceInlineFile{
						"WORKSPACE": {
							Contents: "workspace(name = \"hello\")\n",
						},
						"BUILD": {
							Contents: `
genrule(
    name = "hello",
    outs = ["hello.txt"],
    cmd = "echo 'hello from bazel' > $@",
)
`,
						},
						"hello.txt": {
							Contents: "hello\n",
						},
					},
				},
			},
		}

		cmds = append([]string{"set -ex;"}, cmds...)
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: strings.Join(cmds, "\n"),
		})

		return spec
	}

	t.Run("local", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		randKey := getRand()

		dirCmd := "dir=$(grep disk_cache /etc/bazel.bazelrc | tail -n 1 | awk -F'=' '{ print $2 }')"
		// Write to the bazel cache using bazel itself
		spec := newSpec(randKey, dirCmd, `[ ! -d "${dir}/ac" ]`, `[ ! -d "${dir}/cas" ]`, "cd src; bazel build //:hello")
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
			solveT(ctx, t, client, sr)
		})
		// Now validate that bazel wrote to the cache
		spec = newSpec(randKey, dirCmd, `[ -d "${dir}/ac" ]`, `[ -d "${dir}/cas" ]`)

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("remote", func(t *testing.T) {
		if bzlPkg == "bazel-bootstrap" {
			// bazel-bootstrap is a minimal bazel package that does not support remote caching
			t.Skip("bazel remote cache not available for bazel-bootstrap")
		}

		t.Parallel()
		ctx := startTestSpan(ctx, t)

		randKey := getRand()

		// Setup the bazel "remote" cache server.
		var pipeL socketprovider.PipeListener
		defer pipeL.Close()

		srv := grpc.NewServer()
		cache := bazeltest.NewRemoteCache()
		bazeltest.RegisterRemoteCache(srv, cache)

		go srv.Serve(&pipeL) //nolint:errcheck
		defer srv.Stop()

		// socket path format is 'remote_cache=unix:/path/to/socket
		sockPathCmd := "set -euxo pipefail; sock_path=$(grep remote_cache /etc/bazel.bazelrc | tail -n 1 | awk -F'=' '{ print $2 }' | awk -F':' '{ print $2 }')"
		spec := newSpec(randKey, sockPathCmd, `set -ux; [ -S "${sock_path}" ]`, "cd src; bazel build //:hello")
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
			solveT(ctx, t, client, sr)
		}, testenv.WithSocketProxies(socketprovider.ProxyConfig{
			ID:     dalec.BazelDefaultSocketID,
			Dialer: pipeL.Dialer,
		}))

		assert.Assert(t, cache.Called.Load(), "expected bazel to write to the cache")
	})
}

func testAutoGobuildCache(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	specWithCommand := func(cmd string) *dalec.Spec {
		spec := newSimpleSpec()
		spec.Dependencies = &dalec.PackageDependencies{}
		spec.Dependencies.Build = map[string]dalec.PackageConstraints{
			cfg.GetPackage("golang"): {},
		}
		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: cmd,
		})
		return spec
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -d \"$GOCACHE\" ]; echo hello > ${GOCACHE}/hello\n")

		spec := specWithCommand(buf.String())
		// Set ignore cache to make sure we always run the command so the cache is guaranteed to be populated
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
		solveT(ctx, t, client, sr)

		buf.Reset()
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -d \"$GOCACHE\" ]; grep hello ${GOCACHE}/hello\n")
		spec = specWithCommand(buf.String())

		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Now disable the auto gobuild cache
		spec = specWithCommand("[ -z \"${GOCACHE}\" ]")
		spec.Build.Caches = []dalec.CacheConfig{
			{GoBuild: &dalec.GoBuildCache{Disabled: true}},
		}
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Also make sure there is no autocache when there is no golang dependency
		spec = specWithCommand("[ -z \"${GOCACHE}\" ]")
		spec.Dependencies = nil
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)
	})
}

func testRustCache(ctx context.Context, t *testing.T, cfg targetConfig) {
	ctx = startTestSpan(ctx, t)

	specWithCommand := func(cmd string) *dalec.Spec {
		spec := newSimpleSpec()
		spec.Dependencies = &dalec.PackageDependencies{
			Build: make(map[string]dalec.PackageConstraints),
		}

		// Handle space-separated package lists from PackageOverrides
		rustPackages := cfg.GetPackage("rust")
		for _, pkg := range strings.Fields(rustPackages) {
			spec.Dependencies.Build[pkg] = dalec.PackageConstraints{}
		}

		spec.Build.Steps = append(spec.Build.Steps, dalec.BuildStep{
			Command: cmd,
		})
		return spec
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		// Test no cache
		spec := specWithCommand("[ -z \"${SCCACHE_DIR}\" ] && [ -z \"${RUSTC_WRAPPER}\" ]")
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Test explicitly enabled cargo cache with RUSTC_WRAPPER
		buf := bytes.NewBuffer(nil)
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -n \"$RUSTC_WRAPPER\" ]\n")
		buf.WriteString("[ -x \"$RUSTC_WRAPPER\" ]\n")
		buf.WriteString("echo \"RUSTC_WRAPPER is set to: $RUSTC_WRAPPER\"\n")

		spec = specWithCommand(buf.String())
		spec.Build.Caches = []dalec.CacheConfig{
			{RustSCCache: &dalec.SCCache{}},
		}
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Test the sccache functionality and that cache directory works
		buf.Reset()
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -d \"$SCCACHE_DIR\" ]; echo hello > ${SCCACHE_DIR}/hello\n")

		spec = specWithCommand(buf.String())
		spec.Build.Caches = []dalec.CacheConfig{
			{RustSCCache: &dalec.SCCache{}},
		}
		// Set ignore cache to make sure we always run the command so the cache is guaranteed to be populated
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package), withIgnoreCache(targets.IgnoreCacheKeyPkg))
		solveT(ctx, t, client, sr)

		buf.Reset()
		buf.WriteString("set -ex;\n")
		buf.WriteString("[ -d \"$SCCACHE_DIR\" ]; grep hello ${SCCACHE_DIR}/hello\n")
		spec = specWithCommand(buf.String())
		spec.Build.Caches = []dalec.CacheConfig{
			{RustSCCache: &dalec.SCCache{}},
		}

		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Test that rust sccache is enabled when specified in config
		spec = specWithCommand("[ -n \"${SCCACHE_DIR}\" ] && [ -n \"${RUSTC_WRAPPER}\" ]")
		spec.Build.Caches = []dalec.CacheConfig{
			{RustSCCache: &dalec.SCCache{}},
		}
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)

		// Also make sure there is no autocache when there is no rust dependency
		spec = specWithCommand("[ -z \"${SCCACHE_DIR}\" ] && [ -z \"${RUSTC_WRAPPER}\" ]")
		spec.Dependencies = nil
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
		solveT(ctx, t, client, sr)
	})
}
