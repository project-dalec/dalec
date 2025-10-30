package test

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cavaliergopher/rpm"
	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	"github.com/moby/go-archive/compression"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	pkgerrors "github.com/pkg/errors"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/frontend/pkg/bkfs"
	"golang.org/x/exp/maps"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/skip"
	"pault.ag/go/debian/deb"
)

type workerConfig struct {
	// CreateRepo takes in a state which is the output of the sign target,
	// as well as optional state options for additional configuration.
	// the output [llb.StateOption] should install the repo into the worker image.
	CreateRepo func(st llb.State, repoPath string, opts ...llb.StateOption) llb.StateOption
	SignRepo   func(st llb.State, repoPath string) llb.StateOption
	// ContextName is the name of the worker context that the build target will use
	// to see if a custom worker is provided in a context
	ContextName    string
	TestRepoConfig func(keyPath, repoPath string) map[string]dalec.Source
	Platform       *ocispecs.Platform
	SysextWorker   func(worker llb.State, opts ...llb.ConstraintsOpt) llb.State
}

type targetConfig struct {
	// Key is the base name for the distribution target.
	Key string
	// Package is the target for creating a package.
	Package string
	// Container is the target for creating a container
	Container string
	// Worker is the target for creating the worker image.
	Worker string
	// Sysext is the target for creating a systemd system extension.
	Sysext string

	// FormatDepEqual, when set, alters the provided dependency version to match
	// what is necessary for the target distro to set a dependency for an equals
	// operator.
	FormatDepEqual func(ver, rev string) string

	// Given a spec, list all files (including the full path) that are expected
	// to be sent to be signed.
	ListExpectedSignFiles func(*dalec.Spec, ocispecs.Platform) []string

	// PackageOverrides is useful for replacing packages used in tests (such as `golang`)
	// with alternative ones.
	PackageOverrides map[string]string
}

func (cfg *targetConfig) GetPackage(name string) string {
	updated, ok := cfg.PackageOverrides[name]
	if ok {
		return updated
	}
	return name
}

const noPackageAvailable = ""

type testLinuxConfig struct {
	Target     targetConfig
	LicenseDir string
	SystemdDir struct {
		Units   string
		Targets string
	}
	Libdir  string
	Worker  workerConfig
	Release OSRelease

	SkipStripTest bool

	Platforms         []ocispecs.Platform
	PackageOutputPath func(spec *dalec.Spec, platform ocispecs.Platform) string
}

type OSRelease struct {
	ID        string
	VersionID string
}

func (cfg *testLinuxConfig) GetPackage(name string) string {
	return cfg.Target.GetPackage(name)
}

func rpmTargetOutputPath(id string) func(spec *dalec.Spec, platform ocispecs.Platform) string {
	return func(spec *dalec.Spec, platform ocispecs.Platform) string {
		var arch string
		switch platform.Architecture {
		case "amd64":
			arch = "x86_64"
		case "arm64":
			arch = "aarch64"
		case "arm":
			// this is not perfect, but all we really support in our tests for now, so its fine.
			arch = "armv7l"
		}
		return fmt.Sprintf("/RPMS/%s/%s-%s-%s.%s.%s.rpm", arch, spec.Name, spec.Version, spec.Revision, id, arch)
	}
}

func debTargetOutputPath(id string) func(spec *dalec.Spec, platform ocispecs.Platform) string {
	return func(spec *dalec.Spec, platform ocispecs.Platform) string {
		arch := platform.Architecture
		if arch == "arm" {
			// this is not perfect, but all we really support in our tests for now, so its fine.
			arch = "armhf"
		}
		return fmt.Sprintf("%s_%s-%s_%s.deb", spec.Name, spec.Version, id+"u"+spec.Revision, arch)
	}
}

func testLinuxDistro(ctx context.Context, t *testing.T, testConfig testLinuxConfig) {
	t.Run("Fail when non-zero exit code during build", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := dalec.Spec{
			Name:        "test-build-commands-fail",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing builds commands that fail cause the whole build to fail",
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "exit 42",
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(testConfig.Target.Container))
			sr.Evaluate = true
			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
		})
	})

	t.Run("target-prebuilt-packages", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testPrebuiltPackages(ctx, t, testConfig)
	})

	t.Run("test-dalec-empty-artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testEmptyArtifacts(ctx, t, testConfig.Target)
	})

	t.Run("test-dalec-single-artifact", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testArtifactsAtSpecLevel(ctx, t, testConfig.Target)
	})

	t.Run("test-dalec-multiple-artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testTargetArtifactsTakePrecedence(ctx, t, testConfig.Target)
	})

	t.Run("container", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		const src2Patch3File = "patch3"
		src2Patch3Content := []byte(`
diff --git a/file3 b/file3
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file3
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added another new file"
`)

		src2Patch4Content := []byte(`
diff --git a/file4 b/file4
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file4
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added yet another new file"
`)

		src2Patch5Content := []byte(`
diff --git a/file5 b/file5
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file5
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added yet again...another new file"
`)

		const src2Patch4File = "patches/patch4"
		const src2Patch5File = "patches/patch5"
		const patchContextName = "patch-context"

		patchContext := llb.Scratch().
			File(llb.Mkfile(src2Patch3File, 0o600, src2Patch3Content)).
			File(llb.Mkdir("patches", 0o755)).
			File(llb.Mkfile(src2Patch4File, 0o600, src2Patch4Content)).
			File(llb.Mkfile(src2Patch5File, 0o600, src2Patch5Content))

		spec := dalec.Spec{
			Name:        "test-container-build",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target",
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"file1": {Contents: "file1 contents\n"},
							},
						},
					},
				},
				"src2-patch1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: `
diff --git a/file1 b/file1
index 84d55c5..22b9b11 100644
--- a/file1
+++ b/file1
@@ -1 +1 @@
-file1 contents
+file1 contents patched
`,
						},
					},
				},
				"src2-patch2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"the-patch": {
									Contents: `
diff --git a/file2 b/file2
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file2
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added a new file"
`,
								},
							},
						},
					},
				},
				"src2-patch3": {
					Context: &dalec.SourceContext{
						Name: patchContextName,
					},
				},
				"src2-patch4": {
					Context: &dalec.SourceContext{
						Name: patchContextName,
					},
					Includes: []string{src2Patch4File},
				},
				"src2-patch5": {
					Context: &dalec.SourceContext{
						Name: patchContextName,
					},
					Path: src2Patch5File,
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho goodbye",
							Permissions: 0o700,
						},
					},
				},
			},
			Patches: map[string][]dalec.PatchSpec{
				"src2": {
					{Source: "src2-patch1"},
					{Source: "src2-patch2", Path: "the-patch"},
					{Source: "src2-patch3", Path: src2Patch3File},
					{Source: "src2-patch4", Path: src2Patch4File},
					{Source: "src2-patch5", Path: filepath.Base(src2Patch5File)},
				},
			},

			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"bash":      {},
					"coreutils": {},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					// These are "build" steps where we aren't really building things just verifying
					// that sources are in the right place and have the right permissions and content
					{
						// file added by patch
						Command: "test -f ./src1",
					},
					{
						Command: "test -x ./src1",
					},
					{
						Command: "test ! -d ./src1",
					},
					{
						Command: "./src1 | grep 'hello world'",
					},
					{
						// file added by patch
						Command: "ls -lh ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file2",
					},
					{
						Command: "grep 'Added a new file' ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file3",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file3",
					},
					{
						Command: "grep 'Added another new file' ./src2/file3",
					},
					{
						// Test that a multiline command works with env vars
						Env: map[string]string{
							"FOO": "foo",
							"BAR": "bar",
						},
						Command: `
echo "${FOO}_0" > foo0.txt
echo "${FOO}_1" > foo1.txt
echo "$BAR" > bar.txt
`,
					},
				},
			},

			Image: &dalec.ImageConfig{
				Post: &dalec.PostInstall{
					Symlinks: map[string]dalec.SymlinkTarget{
						"/usr/bin/src1": {
							Path: "/src1",
							User: "need",
						},
						"/usr/bin/src2": {
							Paths: []string{"/non/existing/dir/src2"},
							Group: "coffee",
						},
						"/usr/bin/src3": {
							Paths: []string{"/non/existing/dir/src3", "/non/existing/dir2/src3"},
							User:  "need",
							Group: "coffee",
						},
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1":       {},
					"src2/file2": {},
					"src3":       {},
					// These are files we created in the build step
					// They aren't really binaries but we want to test that they are created and have the right content
					"foo0.txt": {},
					"foo1.txt": {},
					"bar.txt":  {},
				},
				Links: []dalec.ArtifactSymlinkConfig{
					{
						Source: "/usr/bin/src3",
						Dest:   "/bin/owned-link",
						User:   "need",
						Group:  "coffee",
					},
					{
						Source: "/usr/bin/src2/file2",
						Dest:   "/bin/owned-link2",
						User:   "need",
					},
					{
						Source: "/usr/bin/src1",
						Dest:   "/bin/owned-link3",
						Group:  "coffee",
					},
					{
						Source: "/usr/bin/src1",
						Dest:   "/bin/owned-link4",
						User:   "nobody",
					},
				},
				Users: []dalec.AddUserConfig{
					{
						Name: "need",
					},
				},
				Groups: []dalec.AddGroupConfig{
					{
						Name: "coffee",
					},
				},
			},

			Tests: []*dalec.TestSpec{
				{
					Name: "Verify source mounts work",
					Mounts: []dalec.SourceMount{
						{
							Dest: "/foo",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: "hello world",
									},
								},
							},
						},
						{
							Dest: "/nested/foo",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: "hello world nested",
									},
								},
							},
						},
						{
							Dest: "/dir",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"foo": {Contents: "hello from dir"},
										},
									},
								},
							},
						},
						{
							Dest: "/nested/dir",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"foo": {Contents: "hello from nested dir"},
										},
									},
								},
							},
						},
					},
					Steps: []dalec.TestStep{
						{
							Command: "/bin/sh -c 'cat /foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello world"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/bin/sh -c 'cat /nested/foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello world nested"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/bin/sh -c 'cat /dir/foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello from dir"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/bin/sh -c 'cat /nested/dir/foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello from nested dir"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
					},
				},
				{
					Name: "Check that the binary artifacts execute and provide the expected output",
					Steps: []dalec.TestStep{
						{
							Command: "/usr/bin/src1",
							Stdout:  dalec.CheckOutput{Equals: "hello world\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/usr/bin/file2",
							Stdout:  dalec.CheckOutput{Equals: "Added a new file\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
					},
				},
				{
					Name: "Check that multi-line command (from build step) with env vars propagates env vars to whole command",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/bin/foo0.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_0\n"}},
						"/usr/bin/foo1.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_1\n"}},
						"/usr/bin/bar.txt":  {CheckOutput: dalec.CheckOutput{StartsWith: "bar\n"}},
					},
				},
				{
					Name: "Post-install symlinks should be created and have correct ownership",
					Files: map[string]dalec.FileCheckOutput{
						"/src1":                  {},
						"/non/existing/dir/src3": {},
					},
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -exc 'test -L /src1'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /src1)\" = \"/usr/bin/src1\"'"},
						{Command: "/bin/bash -exc 'test -L /non/existing/dir/src2'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /non/existing/dir/src2)\" = \"/usr/bin/src2\"'"},
						{Command: "/bin/bash -exc 'test -L /non/existing/dir/src3'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /non/existing/dir/src3)\" = \"/usr/bin/src3\"'"},
						{Command: "/bin/bash -exc 'test -L /non/existing/dir2/src3'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /non/existing/dir2/src3)\" = \"/usr/bin/src3\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=0; LINK_OWNER=$(stat -c \"%u:%g\" /src1); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'NEED_UID=0; COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /non/existing/dir/src2); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /non/existing/dir/src3); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /non/existing/dir2/src3); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/src1", Stdout: dalec.CheckOutput{Equals: "hello world\n"}, Stderr: dalec.CheckOutput{Empty: true}},
						{Command: "/non/existing/dir/src3", Stdout: dalec.CheckOutput{Equals: "goodbye\n"}, Stderr: dalec.CheckOutput{Empty: true}},
						{Command: "/non/existing/dir2/src3", Stdout: dalec.CheckOutput{Equals: "goodbye\n"}, Stderr: dalec.CheckOutput{Empty: true}},
					},
				},
				{
					Name: "Check /etc/os-release",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/os-release": {
							CheckOutput: dalec.CheckOutput{
								Matches: []string{
									// Some distros have quotes around the values
									// Regex is to match the values with or without quotes
									// "(?m)" enables multi-line mode so that ^ and $ match the start and end of lines rather than the full document.
									//
									// Due to these values getting processed for build args, quotes are stripped unless they are escaped.
									`(?m)^ID=(\")?` + testConfig.Release.ID + `(\")?`,
									`(?m)^VERSION_ID=(\")?` + testConfig.Release.VersionID + `(\")?`,
								},
							},
						},
					},
				},
				{
					Name: "Artifact symlinks should have correct ownership",
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -exc 'test -L /bin/owned-link'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link)\" = \"/usr/bin/src3\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'test -L /bin/owned-link2'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link2)\" = \"/usr/bin/src2/file2\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=0; LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link2); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'test -L /bin/owned-link3'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link3)\" = \"/usr/bin/src1\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=0; COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link3); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'test -L /bin/owned-link4'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link4)\" = \"/usr/bin/src1\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd nobody | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link4); [ \"$LINK_OWNER\" = \"$NEED_UID:0\" ]'"},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(testConfig.Target.Container),
				withBuildContext(ctx, t, patchContextName, patchContext),
			)
			sr.Evaluate = true

			beforeBuild := time.Now()
			res := solveT(ctx, t, gwc, sr)

			dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
			assert.Assert(t, ok, "result metadata should contain an image config: available metadata: %s", strings.Join(maps.Keys(res.Metadata), ", "))

			var cfg dalec.DockerImageSpec
			assert.Assert(t, json.Unmarshal(dt, &cfg))
			assert.Check(t, cfg.Created.After(beforeBuild))
			assert.Check(t, cfg.Created.Before(time.Now()))

			// Make sure the test framework was actually executed by the build target.
			// This appends a test case so that is expected to fail and as such cause the build to fail.
			spec.Tests = append(spec.Tests, &dalec.TestSpec{
				Name: "Test framework should be executed",
				Steps: []dalec.TestStep{
					{Command: "/bin/sh -c 'echo this command should fail; exit 42'"},
				},
			})

			// update the spec in the solve request
			withSpec(ctx, t, &spec)(&newSolveRequestConfig{req: &sr})

			_, err := gwc.Solve(ctx, sr)
			if err == nil {
				t.Fatal("expected test spec to run with error but got none")
			}
		})
	})

	t.Run("sysext", func(t *testing.T) {
		skip.If(t, testConfig.Target.Sysext == "", "skipping test as it is not supported for this config")

		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		const src2Patch3File = "patch3"
		src2Patch3Content := []byte(`
diff --git a/file3 b/file3
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file3
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added another new file"
`)

		src2Patch4Content := []byte(`
diff --git a/file4 b/file4
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file4
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added yet another new file"
`)

		src2Patch5Content := []byte(`
diff --git a/file5 b/file5
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file5
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added yet again...another new file"
`)

		const src2Patch4File = "patches/patch4"
		const src2Patch5File = "patches/patch5"
		const patchContextName = "patch-context"

		patchContext := llb.Scratch().
			File(llb.Mkfile(src2Patch3File, 0o600, src2Patch3Content)).
			File(llb.Mkdir("patches", 0o755)).
			File(llb.Mkfile(src2Patch4File, 0o600, src2Patch4Content)).
			File(llb.Mkfile(src2Patch5File, 0o600, src2Patch5Content))

		spec := dalec.Spec{
			Name:        "test-sysext-build",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing sysext target",
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"file1": {Contents: "file1 contents\n"},
							},
						},
					},
				},
				"src2-patch1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: `
diff --git a/file1 b/file1
index 84d55c5..22b9b11 100644
--- a/file1
+++ b/file1
@@ -1 +1 @@
-file1 contents
+file1 contents patched
`,
						},
					},
				},
				"src2-patch2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"the-patch": {
									Contents: `
diff --git a/file2 b/file2
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file2
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added a new file"
`,
								},
							},
						},
					},
				},
				"src2-patch3": {
					Context: &dalec.SourceContext{
						Name: patchContextName,
					},
				},
				"src2-patch4": {
					Context: &dalec.SourceContext{
						Name: patchContextName,
					},
					Includes: []string{src2Patch4File},
				},
				"src2-patch5": {
					Context: &dalec.SourceContext{
						Name: patchContextName,
					},
					Path: src2Patch5File,
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho goodbye",
							Permissions: 0o700,
						},
					},
				},
			},
			Patches: map[string][]dalec.PatchSpec{
				"src2": {
					{Source: "src2-patch1"},
					{Source: "src2-patch2", Path: "the-patch"},
					{Source: "src2-patch3", Path: src2Patch3File},
					{Source: "src2-patch4", Path: src2Patch4File},
					{Source: "src2-patch5", Path: filepath.Base(src2Patch5File)},
				},
			},

			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"bash":      {},
					"coreutils": {},
				},
				Sysext: map[string]dalec.PackageConstraints{
					"zsh":  {Version: []string{">= 3", "< 99"}},
					"zstd": {Version: []string{">= 1.5.0"}},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					// These are "build" steps where we aren't really building things just verifying
					// that sources are in the right place and have the right permissions and content
					{
						// file added by patch
						Command: "test -f ./src1",
					},
					{
						Command: "test -x ./src1",
					},
					{
						Command: "test ! -d ./src1",
					},
					{
						Command: "./src1 | grep 'hello world'",
					},
					{
						// file added by patch
						Command: "ls -lh ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file2",
					},
					{
						Command: "grep 'Added a new file' ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file3",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file3",
					},
					{
						Command: "grep 'Added another new file' ./src2/file3",
					},
					{
						// Test that a multiline command works with env vars
						Env: map[string]string{
							"FOO": "foo",
							"BAR": "bar",
						},
						Command: `
echo "${FOO}_0" > foo0.txt
echo "${FOO}_1" > foo1.txt
echo "$BAR" > bar.txt
`,
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1":       {},
					"src2/file2": {},
					"src3":       {},
					// These are files we created in the build step
					// They aren't really binaries but we want to test that they are created and have the right content
					"foo0.txt": {},
					"foo1.txt": {},
					"bar.txt":  {},
				},
				Links: []dalec.ArtifactSymlinkConfig{
					{
						Source: "/usr/bin/src3",
						Dest:   "/bin/owned-link",
						User:   "need",
						Group:  "coffee",
					},
					{
						Source: "/usr/bin/src2/file2",
						Dest:   "/bin/owned-link2",
						User:   "need",
					},
					{
						Source: "/usr/bin/src1",
						Dest:   "/bin/owned-link3",
						Group:  "coffee",
					},
					{
						Source: "/usr/bin/src1",
						Dest:   "/bin/owned-link4",
						User:   "nobody",
					},
				},
				Users: []dalec.AddUserConfig{
					{
						Name: "need",
					},
				},
				Groups: []dalec.AddGroupConfig{
					{
						Name: "coffee",
					},
				},
			},

			Tests: []*dalec.TestSpec{
				{
					Name: "Verify source mounts work",
					Mounts: []dalec.SourceMount{
						{
							Dest: "/foo",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: "hello world",
									},
								},
							},
						},
						{
							Dest: "/nested/foo",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: "hello world nested",
									},
								},
							},
						},
						{
							Dest: "/dir",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"foo": {Contents: "hello from dir"},
										},
									},
								},
							},
						},
						{
							Dest: "/nested/dir",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"foo": {Contents: "hello from nested dir"},
										},
									},
								},
							},
						},
					},
					Steps: []dalec.TestStep{
						{
							Command: "/bin/sh -c 'cat /foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello world"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/bin/sh -c 'cat /nested/foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello world nested"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/bin/sh -c 'cat /dir/foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello from dir"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/bin/sh -c 'cat /nested/dir/foo'",
							Stdout:  dalec.CheckOutput{Equals: "hello from nested dir"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
					},
				},
				{
					Name: "Check that the binary artifacts execute and provide the expected output",
					Steps: []dalec.TestStep{
						{
							Command: "/usr/bin/src1",
							Stdout:  dalec.CheckOutput{Equals: "hello world\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/usr/bin/file2",
							Stdout:  dalec.CheckOutput{Equals: "Added a new file\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
					},
				},
				{
					Name: "Check that multi-line command (from build step) with env vars propagates env vars to whole command",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/bin/foo0.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_0\n"}},
						"/usr/bin/foo1.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_1\n"}},
						"/usr/bin/bar.txt":  {CheckOutput: dalec.CheckOutput{StartsWith: "bar\n"}},
					},
				},
				{
					Name: "Check /etc/os-release",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/os-release": {
							CheckOutput: dalec.CheckOutput{
								Matches: []string{
									// Some distros have quotes around the values
									// Regex is to match the values with or without quotes
									// "(?m)" enables multi-line mode so that ^ and $ match the start and end of lines rather than the full document.
									//
									// Due to these values getting processed for build args, quotes are stripped unless they are escaped.
									`(?m)^ID=(\")?` + testConfig.Release.ID + `(\")?`,
									`(?m)^VERSION_ID=(\")?` + testConfig.Release.VersionID + `(\")?`,
								},
							},
						},
					},
				},
				{
					Name: "Artifact symlinks should have correct ownership",
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -exc 'test -L /bin/owned-link'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link)\" = \"/usr/bin/src3\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'test -L /bin/owned-link2'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link2)\" = \"/usr/bin/src2/file2\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd need | cut -d: -f3); COFFEE_GID=0; LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link2); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'test -L /bin/owned-link3'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link3)\" = \"/usr/bin/src1\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=0; COFFEE_GID=$(getent group coffee | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link3); [ \"$LINK_OWNER\" = \"$NEED_UID:$COFFEE_GID\" ]'"},
						{Command: "/bin/bash -exc 'test -L /bin/owned-link4'"},
						{Command: "/bin/bash -exc 'test \"$(readlink /bin/owned-link4)\" = \"/usr/bin/src1\"'"},
						{Command: "/bin/bash -exc 'NEED_UID=$(getent passwd nobody | cut -d: -f3); LINK_OWNER=$(stat -c \"%u:%g\" /bin/owned-link4); [ \"$LINK_OWNER\" = \"$NEED_UID:0\" ]'"},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(testConfig.Target.Sysext),
				withBuildContext(ctx, t, patchContextName, patchContext),
			)
			sr.Evaluate = true

			beforeBuild := time.Now()
			res := solveT(ctx, t, gwc, sr)

			dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
			assert.Assert(t, ok, "result metadata should contain an image config: available metadata: %s", strings.Join(maps.Keys(res.Metadata), ", "))

			var cfg dalec.DockerImageSpec
			assert.Assert(t, json.Unmarshal(dt, &cfg))
			assert.Check(t, cfg.Created.After(beforeBuild))
			assert.Check(t, cfg.Created.Before(time.Now()))

			// Make sure the test framework was actually executed by the build target.
			// This appends a test case so that is expected to fail and as such cause the build to fail.
			spec.Tests = append(spec.Tests, &dalec.TestSpec{
				Name: "Test framework should be executed",
				Steps: []dalec.TestStep{
					{Command: "/bin/sh -c 'echo this command should fail; exit 42'"},
				},
			})

			// update the spec in the solve request
			withSpec(ctx, t, &spec)(&newSolveRequestConfig{req: &sr})

			_, solveErr := gwc.Solve(ctx, sr)
			if solveErr == nil {
				t.Fatal("expected test spec to run with error but got none")
			}

			// Map Docker to systemd architecture. Some (e.g. arm64) are the
			// same and are covered by the default case.
			var systemdArch string
			switch cfg.Platform.Architecture {
			case "amd64":
				systemdArch = "x86-64"
			default:
				systemdArch = cfg.Platform.Architecture
			}

			ref, refErr := res.SingleRef()
			if refErr != nil {
				t.Fatal(refErr)
			}

			expectedPath := fmt.Sprintf("/test-sysext-build-v0.0.1-1-%s-%s.raw", testConfig.Target.Key, systemdArch)
			_, statErr := ref.StatFile(ctx, gwclient.StatRequest{Path: expectedPath})
			if statErr != nil {
				t.Fatalf("expected sysext image not found: %v", statErr)
			}

			sr = newSolveRequest(withBuildTarget(testConfig.Target.Worker), withSpec(ctx, t, nil))
			worker := testConfig.Worker.SysextWorker(reqToState(ctx, gwc, sr, t))

			pc := dalec.Platform(&cfg.Platform)
			var opts []llb.ConstraintsOpt
			opts = append(opts, pc)

			state, stateErr := ref.ToState()
			if stateErr != nil {
				t.Fatal(stateErr)
			}

			output := worker.Run(
				llb.Args([]string{"fsck.erofs", "--extract=/output", "/input" + expectedPath}),
				llb.AddMount("/input", state, llb.Readonly),
				dalec.WithConstraints(opts...),
			).AddMount("/output", llb.Scratch())

			def, defErr := output.Marshal(ctx, pc)
			if defErr != nil {
				t.Fatalf("error marshalling llb: %v", defErr)
			}

			res, resErr := gwc.Solve(ctx, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})
			if resErr != nil {
				t.Fatal(resErr)
			}

			ref, refErr = res.SingleRef()
			if refErr != nil {
				t.Fatal(refErr)
			}
			if evalErr := ref.Evaluate(ctx); evalErr != nil {
				t.Fatalf("error extracting sysext: %v", evalErr)
			}

			for _, file := range []string{"/usr/bin/zsh", "/usr/bin/zstd"} {
				_, statErr = ref.StatFile(ctx, gwclient.StatRequest{Path: file})
				if statErr != nil {
					t.Fatalf("expected file in sysext not found: %v", statErr)
				}
			}

			// zlib is required by zstd, but it shouldn't be pulled into the
			// sysext. Its installed location varies by distro.
			for _, file := range []string{"/usr/bin/bash", "/usr/bin/ls", "/usr/lib/libz.so.1", "/usr/lib/x86_64-linux-gnu/libz.so.1"} {
				_, statErr = ref.StatFile(ctx, gwclient.StatRequest{Path: file})
				if statErr == nil {
					t.Fatalf("unexpected file in sysext found: %s", file)
				}
			}
		})
	})

	t.Run("signing", linuxSigningTests(ctx, testConfig))

	t.Run("test systemd unit single", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/project-dalec/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"simple.service": {
									Contents: `
[Unit]
Description=Phony Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/service
Restart=always

[Install]
WantedBy=multi-user.target
`,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"src/simple.service": {
							Enable: true,
						},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(testConfig.SystemdDir.Units, "system/simple.service"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
							Permissions: 0o644,
						},
						// symlinked file in multi-user.target.wants should point to simple.service.
						filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/simple.service"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})

		// Test to ensure disabling works by default
		spec.Artifacts.Systemd = &dalec.SystemdConfiguration{
			Units: map[string]dalec.SystemdUnitConfig{
				"src/simple.service": {},
			},
		}
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "Check service files",
				Files: map[string]dalec.FileCheckOutput{
					filepath.Join(testConfig.SystemdDir.Units, "system/simple.service"): {
						CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						Permissions: 0o644,
					},
					filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/simple.service"): {
						NotExist: true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})

		// Test to ensure unit can be installed under a different name
		spec.Artifacts.Systemd = &dalec.SystemdConfiguration{
			Units: map[string]dalec.SystemdUnitConfig{
				"src/simple.service": {
					Name: "phony.service",
				},
			},
		}

		spec.Tests = []*dalec.TestSpec{
			{
				Name: "Check service files",
				Files: map[string]dalec.FileCheckOutput{
					filepath.Join(testConfig.SystemdDir.Units, "system/phony.service"): {
						CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						Permissions: 0o644,
					},
					filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/phony.service"): {
						NotExist: true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("test systemd unit multiple components", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/project-dalec/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"foo.service": {
									Contents: `
[Unit]
Description=Foo Service
After=network.target foo.socket
Requires=foo.socket

[Service]
Type=simple
ExecStart=/usr/bin/foo
Restart=always

[Install]
WantedBy=multi-user.target
`,
								},

								"foo.socket": {
									Contents: `
[Unit]
Description=foo socket
PartOf=foo.service

[Socket]
ListenStream=127.0.0.1:8080

[Install]
WantedBy=sockets.target
									`,
								},
								"foo.conf": {
									Contents: `
[Service]
Environment="FOO_ARGS=--some-foo-arg"
									`,
								},
								"env.conf": {
									Contents: `
[Service]
Environment="FOO_ARGS=--some-foo-args"
									`,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"src/foo.service": {},
						"src/foo.socket": {
							Enable: true,
						},
					},
					Dropins: map[string]dalec.SystemdDropinConfig{
						"src/foo.conf": {
							Unit: "foo.service",
						},
						"src/env.conf": {
							Unit: "foo.socket",
						},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/foo"}},
							Permissions: 0o644,
						},
						filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/foo.service"): {
							NotExist: true,
						},
						filepath.Join(testConfig.SystemdDir.Targets, "sockets.target.wants/foo.socket"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Description=foo socket"}},
						},
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service.d/foo.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0o644,
						},
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.socket.d/env.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0o644,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("test systemd with only config dropin", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/project-dalec/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"foo.conf": {
									Contents: `
[Service]
Environment="KUBELET_KUBECONFIG_ARGS=--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf"
								`,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Dropins: map[string]dalec.SystemdDropinConfig{
						"src/foo.conf": {
							Unit: "foo.service",
						},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service.d/foo.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0o644,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("go module", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-build-with-gomod",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{},
						},
					},
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"main.go": {Contents: gomodFixtureMain},
								"go.mod":  {Contents: gomodFixtureMod},
								"go.sum":  {Contents: gomodFixtureSum},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					testConfig.GetPackage("golang"): {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{Command: "[ -d \"${GOMODCACHE}/github.com/cpuguy83/tar2go@v0.3.1\" ]"},
					{Command: "[ -d ./src ]"},
					{Command: "[ -f ./src/main.go ]"},
					{Command: "[ -f ./src/go.mod ]"},
					{Command: "[ -f ./src/go.sum ]"},
					{Command: "cd ./src && go build"},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("cargo home", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-build-with-cargohome",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target with Cargo",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Cargohome: &dalec.GeneratorCargohome{},
						},
					},
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"Cargo.toml": {Contents: cargoFixtureToml},
								"Cargo.lock": {Contents: cargoFixtureLock},
								"main.rs":    {Contents: cargoFixtureMain},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					testConfig.GetPackage("rust"): {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{Command: "[ -d \"${CARGO_HOME}/registry/\" ]"},
					{Command: "[ -d ./src ]"},
					{Command: "[ -f ./src/Cargo.toml ]"},
					{Command: "[ -f ./src/Cargo.lock ]"},
					{Command: "[ -f ./src/main.rs ]"},
					{Command: "cd ./src && cargo build"},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("pip", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-build-with-pip",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target with pip",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Pip: &dalec.GeneratorPip{},
						},
					},
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"main.py":          {Contents: pipFixtureMain},
								"requirements.txt": {Contents: pipFixtureRequirements},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					testConfig.GetPackage("python3"):     {},
					testConfig.GetPackage("python3-pip"): {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{Command: "[ -d ./src ]"},
					{Command: "[ -f ./src/main.py ]"},
					{Command: "[ -f ./src/requirements.txt ]"},
					{Command: "[ -d ./src/site-packages ]"},
					{Command: "cd ./src; python3 -c \"import sys; sys.path.insert(0, './site-packages'); import certifi; print('certifi imported successfully')\""},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("node npm generator", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testNodeNpmGenerator(ctx, t, testConfig.Target)
	})

	t.Run("test directory creation", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-directory-creation",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"curl": {},
				},
			},
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1": {},
				},
				Directories: &dalec.CreateArtifactDirectories{
					Config: map[string]dalec.ArtifactDirConfig{
						"test": {},
						"testWithPerms": {
							Mode: 0o700,
						},
					},
					State: map[string]dalec.ArtifactDirConfig{
						"one/with/slashes": {},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/etc/test", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/etc/testWithPerms", 0o700); err != nil {
				t.Fatal(err)
			}
			// validatePathAndPermissions doesn't work for container-only users because it runs on the host
			// Ownership for /etc/testWithUsers is validated in the PostInstall test above
			if err := validatePathAndPermissions(ctx, ref, "/var/lib/one/with/slashes", 0o755); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test artifact permissions", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		spec := &dalec.Spec{
			Name:        "test-artifact-permissions",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Sources: map[string]dalec.Source{
				"src-original-perm": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o644,
						},
					},
				},
				"src-change-perm": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"src-dir": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"another_nested_data_file": {
									Contents:    "Hello World!\n",
									Permissions: 0o644,
								},
							},
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src-original-perm": {},
					"src-change-perm": {
						Permissions: 0o755,
					},
					"src-dir/another_nested_data_file": {},
				},
			},
		}
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/bin/src-original-perm", 0o644); err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/bin/src-change-perm", 0o755); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test data file installation", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-data-file-installation",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should install specified data files",
			Sources: map[string]dalec.Source{
				"bin": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"data_dir": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"nested_data_file": {
									Contents:    "this is a file which should end up at the path /usr/share/data_dir/nested_data_file\n",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"another_data_dir": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"another_nested_data_file": {
									Contents:    "this is a file which should end up at the path /usr/share/data_dir/nested_data_file\n",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"another_data_dir2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"another_nested_data_file2": {
									Contents:    "lorem ipsum dolor sit amet\n",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"data_file": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "This is a data file which should end up at /usr/share/data_file\n",
							Permissions: 0o644,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"bin": {},
				},
				DataDirs: map[string]dalec.ArtifactConfig{
					"data_dir": {},
					"another_data_dir": {
						SubPath: "subpath",
					},
					"another_data_dir2": {
						User:        "myuser",
						Group:       "mygroup",
						Permissions: 0o777,
					},
					"data_file": {},
				},
				Users: []dalec.AddUserConfig{
					{Name: "myuser"},
				},
				Groups: []dalec.AddGroupConfig{
					{Name: "mygroup"},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"coreutils": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check data directory ownership in post-install",
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -exc 'ls -ld /usr/share/another_data_dir2 | grep -E \" myuser[[:space:]]+mygroup[[:space:]]\"'"},
						{Command: "/bin/bash -exc 'ls -l /usr/share/another_data_dir2/another_nested_data_file2 | grep -E \" myuser[[:space:]]+mygroup[[:space:]]\"'"},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_dir", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_dir/nested_data_file", 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/subpath/another_data_dir/another_nested_data_file", 0o644); err != nil {
				t.Fatal(err)
			}
			// validatePathAndPermissions doesn't work for container-only users because it runs on the host
			// Ownership for another_data_dir2 is validated in the PostInstall test above
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_file", 0o644); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test libexec file installation", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "libexec-test",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should install specified data files",
			Sources: map[string]dalec.Source{
				"no_name_no_subpath": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"name_only": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"name_and_subpath": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"subpath_only": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"nested_subpath": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"no_name_no_subpath": {},
				},
				Libexec: map[string]dalec.ArtifactConfig{
					"no_name_no_subpath": {},
					"name_only": {
						Name: "this_is_the_name_only",
					},
					"name_and_subpath": {
						SubPath: "subpath",
						Name:    "custom_name",
					},
					"subpath_only": {
						SubPath: "custom",
					},
					"nested_subpath": {
						SubPath: "libexec-test/abcdefg",
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/no_name_no_subpath", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/this_is_the_name_only", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/subpath/custom_name", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/custom/subpath_only", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/libexec-test/abcdefg/nested_subpath", 0o755); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test config files handled", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-config-files-work",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{"curl": {}},
			},
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=goodbye",
							Permissions: 0o700,
						},
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"hello": {
									Contents: "world",
								},
							},
						},
					},
				},
				"src4": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"hello": {
									Contents: "world4",
								},
							},
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"src1": {},
					"src2": {
						SubPath: "sysconfig",
					},
					"src3": {},
					"src4": {
						SubPath: "dirWithSubpath",
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Config Files Should Be Created in correct place",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/src1":           {},
						"/etc/sysconfig/src2": {},
						"/etc/src3/hello": {
							CheckOutput: dalec.CheckOutput{Equals: "world"},
						},
						"/etc/dirWithSubpath/src4/hello": {
							CheckOutput: dalec.CheckOutput{Equals: "world4"},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			sr.Evaluate = true
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("docs and headers and licenses are handled correctly", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-docs-handled",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Docs should be placed",
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src4": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src5": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"header.h": {
									Contents:    "message=hello",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"src6": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"header.h": {
									Contents:    "message=hello",
									Permissions: 0o644,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"src1": {},
					"src2": {
						SubPath: "subpath",
					},
				},
				Licenses: map[string]dalec.ArtifactConfig{
					"src3": {},
					"src4": {
						SubPath: "license-subpath",
					},
				},
				Headers: map[string]dalec.ArtifactConfig{
					// Files with and without ArtifactConfig
					"src1": {
						Name:    "renamed-src1",
						SubPath: "header-subpath-src1",
					},
					"src2": {},
					// Directories with and without ArtifactConfig
					"src5": {
						Name:    "renamed-src5",
						SubPath: "header-subpath-src5",
					},
					"src6": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Doc and lib and header files should be created in correct place",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/share/doc/test-docs-handled/src1":                                        {},
						"/usr/share/doc/test-docs-handled/subpath/src2":                                {},
						filepath.Join(testConfig.LicenseDir, "test-docs-handled/src3"):                 {},
						filepath.Join(testConfig.LicenseDir, "test-docs-handled/license-subpath/src4"): {},
						"/usr/include/header-subpath-src1/renamed-src1":                                {},
						"/usr/include/src2": {},
						"/usr/include/header-subpath-src5/renamed-src5": {
							IsDir: true,
						},
						"/usr/include/src6": {
							IsDir: true,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			sr.Evaluate = true
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("meta package", func(t *testing.T) {
		// Ensure that packages that just install other packages give the expected output

		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "some-meta-thing",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "meta test",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"curl": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)
			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			_, err = ref.StatFile(ctx, gwclient.StatRequest{
				Path: "/usr/bin/curl",
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("custom worker", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testCustomLinuxWorker(ctx, t, testConfig.Target, testConfig.Worker)
	})

	t.Run("pinned build dependencies", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testPinnedBuildDeps(ctx, t, testConfig)
	})

	t.Run("custom repo", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testCustomRepo(ctx, t, testConfig.Worker, testConfig.Target)
	})

	t.Run("test library artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testLinuxLibArtirfacts(ctx, t, testConfig)
	})
	t.Run("test symlink artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testLinuxSymlinkArtifacts(ctx, t, testConfig)
	})
	t.Run("test gomod edits", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testGomodEdits(ctx, t, testConfig)
	})
	t.Run("test image configs", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testImageConfig(ctx, t, testConfig.Target.Container)
	})

	t.Run("test package tests cause build to fail", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testLinuxPackageTestsFail(ctx, t, testConfig)
	})

	t.Run("build network mode", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testBuildNetworkMode(ctx, t, testConfig.Target)
	})

	t.Run("user and group creation", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testUserAndGroupCreation(ctx, t, testConfig.Target)
	})

	t.Run("test dalec target arg is set", func(t *testing.T) {
		testDalecTargetArg(ctx, t, testConfig.Target)
	})

	t.Run("inherited dependencies", func(t *testing.T) {
		t.Parallel()
		testMixGlobalTargetDependencies(ctx, t, testConfig)
	})

	t.Run("disable strip", func(t *testing.T) {
		t.Parallel()
		testDisableStrip(ctx, t, testConfig)
	})

	t.Run("cross platform", func(t *testing.T) {
		t.Parallel()
		testTargetPlatform(ctx, t, testConfig)
	})

	t.Run("package provides", func(t *testing.T) {
		t.Parallel()
		testPackageProvidesReplaces(ctx, t, testConfig)
	})

	t.Run("artifact build cache dir", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testArtifactBuildCacheDir(ctx, t, testConfig.Target)
	})

	t.Run("auto gobuild cache", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testAutoGobuildCache(ctx, t, testConfig.Target)
	})

	t.Run("rust cache", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testRustCache(ctx, t, testConfig.Target)
	})

	t.Run("bazel cache", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testBazelCache(ctx, t, testConfig.Target)
	})

	t.Run("disable auto require", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testDisableAutoRequire(ctx, t, testConfig.Target)
	})
}

func testNodeNpmGenerator(ctx context.Context, t *testing.T, targetCfg targetConfig, opts ...srOpt) {
	spec := &dalec.Spec{
		Name:        "test-build-with-nodenpm-generator",
		Version:     "0.0.1",
		Revision:    "1",
		License:     "MIT",
		Website:     "https://github.com/project-dalec/dalec",
		Vendor:      "Dalec",
		Packager:    "Dalec",
		Description: "Testing container target with node npm generator",
		Sources: map[string]dalec.Source{
			"src": {
				Generate: []*dalec.SourceGenerator{
					{
						NodeMod: &dalec.GeneratorNodeMod{},
					},
				},
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							"package.json": {Contents: npmPackageJson},
							"npm.lock":     {Contents: npmPackageLockJson},
							"index.js":     {Contents: IndexJS},
						},
					},
				},
			},
		},
		Dependencies: &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				targetCfg.GetPackage("npm"): {},
			},
		},
		Build: dalec.ArtifactBuild{
			Steps: []dalec.BuildStep{
				{Command: "[ -f ./src/package.json ]"},
				{Command: "[ -f ./src/npm.lock ]"},
				{Command: "[ -f ./src/index.js ]"},
				{Command: "cd ./src; npm start > result.txt"},
			},
		},
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"src/result.txt": {},
			},
		},
		Tests: []*dalec.TestSpec{
			{
				Name: "Check npm result",
				Files: map[string]dalec.FileCheckOutput{
					"/usr/bin/result.txt": {
						CheckOutput: dalec.CheckOutput{
							Contains: []string{"Lodash chunk: [ [ 1, 2 ], [ 3, 4 ] ]"},
						},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		reqOpts := append([]srOpt{withBuildTarget(targetCfg.Container), withSpec(ctx, t, spec)}, opts...)
		req := newSolveRequest(reqOpts...)
		solveT(ctx, t, client, req)
	})
}

func testCustomLinuxWorker(ctx context.Context, t *testing.T, targetCfg targetConfig, workerCfg workerConfig) {
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		// base package that will be used as a build dependency of the main package.
		depSpec := &dalec.Spec{
			Name:        "dalec-test-package-custom-worker-dep",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"hello.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "hello world!",
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"hello.txt": {},
				},
			},
		}

		// Main package, this should fail to build without a custom worker that has
		// the base package available.
		spec := &dalec.Spec{
			Name:        "test-dalec-custom-worker",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing custom worker images to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					depSpec.Name: {},
				},
			},
		}

		// Make sure the built-in worker can't build this package
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
		_, err := gwc.Solve(ctx, sr)
		if err == nil {
			t.Fatal("expected solve to fail")
		}

		var xErr *moby_buildkit_v1_frontend.ExitError
		if !errors.As(err, &xErr) {
			t.Fatalf("got unexpected error, expected error type %T: %v", xErr, err)
		}

		// Build the base package
		sr = newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(targetCfg.Package))
		pkg := reqToState(ctx, gwc, sr, t)

		// Build the worker target, this will give us the worker image as an output.
		// Note: Currently we need to provide a dalec spec just due to how the router is setup.
		//       The spec can be nil, though, it just needs to be parsable by yaml unmarshaller.
		sr = newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
		worker := reqToState(ctx, gwc, sr, t)

		// Add the base package + repo to the worker
		// This should make it so when dalec installs build deps it can use the package
		// we built above.
		repoPath := filepath.Join("/opt/repo", createRepoSuffix())
		worker = worker.With(workerCfg.CreateRepo(pkg, repoPath))

		// Now build again with our custom worker
		// Note, we are solving the main spec, not depSpec here.
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, workerCfg.ContextName, worker), withBuildTarget(targetCfg.Container))
		solveT(ctx, t, gwc, sr)

		// TODO: we should have a test to make sure this also works with source policies.
		// Unfortunately it seems like there is an issue with the gateway client passing
		// in source policies.
	})
}

func testPinnedBuildDeps(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	pkgName := "dalec-test-package-pinned"

	getTestPackageSpec := func(version string) *dalec.Spec {
		depSpec := &dalec.Spec{
			Name:        pkgName,
			Version:     version,
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"dalec-test-version": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho version: " + version,
							Permissions: 0o755,
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"dalec-test-version": {},
				},
			},
		}

		return depSpec
	}

	depSpecs := []*dalec.Spec{
		getTestPackageSpec("1.1.1"),
		getTestPackageSpec("1.2.0"),
		getTestPackageSpec("1.3.0"),
	}

	// getTestPinnedSpec returns a spec that has a build dependency on the package with the given constraints.
	// and with an included test in the build steps which ensures that the correct version of the
	// package was used.
	getPinnedTestSpec := func(constraints string, expectVersion string) *dalec.Spec {
		return &dalec.Spec{
			Name:        "dalec-test-pinned-build-deps",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing custom worker images to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					pkgName: {
						Version: []string{constraints},
					},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: fmt.Sprintf(`set -x; [ "$(dalec-test-version)" = "version: %s" ]`, expectVersion),
					},
				},
			},
		}
	}

	formatEqualForDistro := func(v, rev string) string {
		if cfg.Target.FormatDepEqual == nil {
			return v
		}
		return cfg.Target.FormatDepEqual(v, rev)
	}

	tests := []struct {
		name        string
		constraints string
		want        string
	}{
		{
			name:        "exact dep available",
			constraints: "== " + formatEqualForDistro("1.1.1", "1"),
			want:        "1.1.1",
		},

		{
			name:        "lt dep available",
			constraints: "< 1.3.0",
			want:        "1.2.0",
		},

		{
			name:        "gt dep available",
			constraints: "> 1.2.0",
			want:        "1.3.0",
		},
	}

	getWorker := func(ctx context.Context, t *testing.T, client gwclient.Client) llb.State {
		// Build the worker target, this will give us the worker image as an output.
		// Note: Currently we need to provide a dalec spec just due to how the router is setup.
		//       The spec can be nil, though, it just needs to be parsable by yaml unmarshaller.
		sr := newSolveRequest(withBuildTarget(cfg.Target.Worker), withSpec(ctx, t, nil))
		w := reqToState(ctx, client, sr, t)

		var pkgs []llb.State
		for _, depSpec := range depSpecs {
			sr := newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(cfg.Target.Package))
			pkg := reqToState(ctx, client, sr, t)
			pkgs = append(pkgs, pkg)
		}
		repoPath := filepath.Join("/opt/repo", createRepoSuffix())
		return w.With(cfg.Worker.CreateRepo(llb.Merge(pkgs), repoPath))
	}

	for _, tt := range tests {
		spec := getPinnedTestSpec(tt.constraints, tt.want)

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(ctx, t)

			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				worker := getWorker(ctx, t, gwc)

				sr := newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, cfg.Worker.ContextName, worker), withBuildTarget(cfg.Target.Container), withPlatformPtr(cfg.Worker.Platform))
				res := solveT(ctx, t, gwc, sr)
				_, err := res.SingleRef()
				if err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func testLinuxLibArtirfacts(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("file", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)

		libDir := "/usr/lib"
		if cfg.Libdir != "" {
			libDir = cfg.Libdir
		}

		spec := &dalec.Spec{
			Name:        "test-library-files",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing library files",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"lib1": {Contents: "this is lib1"},
								"lib2": {Contents: "this is lib2"},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"src/lib1": {},
					"src/lib2": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check that lib files exist under package dir",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(libDir, "lib1"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib1",
						}},
						filepath.Join(libDir, "lib2"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib2",
						}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)
		})
	})

	t.Run("dir", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)
		libDir := "/usr/lib"
		if cfg.Libdir != "" {
			libDir = cfg.Libdir
		}

		spec := &dalec.Spec{
			Name:        "test-library-files",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing library files",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"lib1": {Contents: "this is lib1"},
								"lib2": {Contents: "this is lib2"},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"src/*": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check that lib files exist under package dir",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(libDir, "lib1"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib1",
						}},
						filepath.Join(libDir, "lib2"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib2",
						}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)
		})
	})

	t.Run("mixed", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)

		libDir := "/usr/lib"
		if cfg.Libdir != "" {
			libDir = cfg.Libdir
		}

		spec := &dalec.Spec{
			Name:        "test-library-files",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing library files",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"lib1": {Contents: "this is lib1"},
								"lib2": {Contents: "this is lib2"},
							},
						},
					},
				},
				"lib3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "this is lib3",
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"src/*": {},
					"lib3":  {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check that lib files exist under package dir",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(libDir, "lib1"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib1",
						}},
						filepath.Join(libDir, "lib2"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib2",
						}},
						filepath.Join(libDir, "lib3"): {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib3",
						}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)
		})
	})
}

func testLinuxSymlinkArtifacts(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := &dalec.Spec{
		Name:        "test-symlinks",
		Version:     "0.0.1",
		Revision:    "42",
		Description: "Testing symlinks",
		License:     "MIT",

		Dependencies: &dalec.PackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"bash": {},
			},
		},

		Artifacts: dalec.Artifacts{
			Links: []dalec.ArtifactSymlinkConfig{
				{
					Source: "/bin/sh",
					Dest:   "/bin/dalecsh",
				},
			},
		},
		Tests: []*dalec.TestSpec{
			{
				Name: "Test symlink works",
				Steps: []dalec.TestStep{
					{
						Command: "/bin/dalecsh -c 'echo -n hello world'",
						Stdout:  dalec.CheckOutput{Equals: "hello world"},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
		res := solveT(ctx, t, client, sr)
		_, err := res.SingleRef()
		assert.NilError(t, err)
	})
}

func testImageConfig(ctx context.Context, t *testing.T, target string, opts ...srOpt) {
	spec := &dalec.Spec{
		Name:        "test-image-config",
		Version:     "0.0.1",
		Revision:    "42",
		Description: "Test to make sure image configs are copied over",
		License:     "MIT",
		Image: &dalec.ImageConfig{
			Entrypoint: "some-entrypoint",
			Cmd:        "some-cmd",
			Env: []string{
				"ENV1=VAL1",
				"ENV2=VAL2",
			},
			Labels: map[string]string{
				"label.1": "value1",
				"label.2": "value2",
			},
			Volumes: map[string]struct{}{
				"/some/volume": {},
			},
			WorkingDir: "/some/work/dir",
			StopSignal: "SOME-SIG",
			User:       "some-user",
		},
	}

	envToMap := func(envs []string) map[string]string {
		out := make(map[string]string, len(envs))
		for _, env := range envs {
			k, v, _ := strings.Cut(env, "=")
			out[k] = v
		}
		return out
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		opts = append(opts, withSpec(ctx, t, spec))
		opts = append(opts, withBuildTarget(target))
		sr := newSolveRequest(opts...)
		res := solveT(ctx, t, gwc, sr)

		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		assert.Assert(t, ok, "missing image config in result metadata")

		var img dalec.DockerImageSpec
		err := json.Unmarshal(dt, &img)
		assert.NilError(t, err)

		assert.Check(t, cmp.Equal(strings.Join(img.Config.Entrypoint, " "), spec.Image.Entrypoint))
		assert.Check(t, cmp.Equal(strings.Join(img.Config.Cmd, " "), spec.Image.Cmd))

		// Envs are merged together with the base image
		// So we need to validate that the values we've set are what we expect
		// Often there will be at least one other env for `PATH` we won't check
		expectEnv := envToMap(spec.Image.Env)
		actualEnv := envToMap(img.Config.Env)
		for k, v := range expectEnv {
			assert.Check(t, cmp.Equal(actualEnv[k], v))
		}

		// Labels are merged with the base image
		// So we need to check that the labels we've set are added
		for k, v := range spec.Image.Labels {
			assert.Check(t, cmp.Equal(v, img.Config.Labels[k]))
		}

		// Volumes are merged with the base image
		// So we need to check that the volumes we've set are added
		for k := range spec.Image.Volumes {
			_, ok := img.Config.Volumes[k]
			assert.Check(t, ok, k)
		}

		assert.Check(t, cmp.Equal(img.Config.WorkingDir, spec.Image.WorkingDir))
		assert.Check(t, cmp.Equal(img.Config.StopSignal, spec.Image.StopSignal))
		assert.Check(t, cmp.Equal(img.Config.User, spec.Image.User))
	})
}

func testLinuxPackageTestsFail(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("negative test", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-package-tests",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing package tests",
			License:     "MIT",
			Tests: []*dalec.TestSpec{
				{
					Name: "Test that tests fail the build",
					Files: map[string]dalec.FileCheckOutput{
						"/non-existing-file": {},
					},
				},
				{
					Name: "Test that permissions check fails the build",
					Files: map[string]dalec.FileCheckOutput{
						"/": {Permissions: 0o644, IsDir: true},
					},
				},
				{
					Name: "Test that dir check fails the build",
					Files: map[string]dalec.FileCheckOutput{
						"/": {IsDir: false},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			_, err := client.Solve(ctx, sr)
			assert.ErrorContains(t, err, "expected \"/non-existing-file\" not_exist \"exists=false\"")
			assert.ErrorContains(t, err, "expected \"/\" permissions \"-rw-r--r--\", got \"-rwxr-xr-x\"")
			assert.ErrorContains(t, err, "expected \"/\" is_dir \"ModeFile\", got \"ModeDir\"")

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			_, err = client.Solve(ctx, sr)
			assert.ErrorContains(t, err, "expected \"/non-existing-file\" not_exist \"exists=false\"")
			assert.ErrorContains(t, err, "expected \"/\" permissions \"-rw-r--r--\", got \"-rwxr-xr-x\"")
			assert.ErrorContains(t, err, "expected \"/\" is_dir \"ModeFile\", got \"ModeDir\"")
		})
	})

	t.Run("positive test", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-package-tests",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing package tests",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"test-file": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "hello world",
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Test: map[string]dalec.PackageConstraints{"bash": {}, "grep": {}},
			},
			Artifacts: dalec.Artifacts{
				DataDirs: map[string]dalec.ArtifactConfig{
					"test-file": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Test that tests fail the build",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/share/test-file": {},
						// Make sure dir permissions are chcked correctly.
						"/usr/share": {IsDir: true, Permissions: 0o755},
					},
				},
				{
					Name: "Test that test mounts work",
					Files: map[string]dalec.FileCheckOutput{
						"/tmp/step0": {},
						"/tmp/step1": {},
						"/tmp/step2": {},
						"/tmp/step3": {},
						"/tmp/step4": {},
					},
					Steps: []dalec.TestStep{
						{
							Command: "/bin/sh -ec 'test -f /mount0 > /tmp/step0'",
						},
						{
							Command: "/bin/sh -ec 'test -d /mount1 > /tmp/step1'",
						},
						{
							Command: `/bin/sh -ec 'grep "some file" /mount1/some_file > /tmp/step2'`,
						},
						{
							Command: "/bin/sh -ec 'test -f /mount2 > /tmp/step3'",
						},
						{
							Command: `/bin/sh -ec 'grep "some other file" /mount2 > /tmp/step4'`,
						},
					},
					Mounts: []dalec.SourceMount{
						{
							Dest: "/mount0",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: "mount0",
									},
								},
							},
						},
						{
							Dest: "/mount1",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"some_file": {
												Contents: "some file",
											},
										},
									},
								},
							},
						},
						{
							Dest: "/mount2",
							Spec: dalec.Source{
								Path: "another_file",
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"another_file": {
												Contents: "some other file",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			res := solveT(ctx, t, client, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res = solveT(ctx, t, client, sr)
			_, err = res.SingleRef()
			assert.NilError(t, err)
		})
	})
}

func testUserAndGroupCreation(ctx context.Context, t *testing.T, testCfg targetConfig) {
	spec := newSimpleSpec()

	spec.Artifacts.Groups = []dalec.AddGroupConfig{
		{Name: "testgroup"},
	}
	spec.Artifacts.Users = []dalec.AddUserConfig{
		{Name: "testuser"},
	}

	spec.Tests = []*dalec.TestSpec{
		{
			Files: map[string]dalec.FileCheckOutput{
				"/etc/group": {
					CheckOutput: dalec.CheckOutput{
						Contains: []string{
							"testgroup:x:",
							"testuser:x:",
						},
					},
				},
				"/etc/passwd": {
					CheckOutput: dalec.CheckOutput{
						Contains: []string{"testuser:x:"},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(testCfg.Container))
		res := solveT(ctx, t, client, sr)
		_, err := res.SingleRef()
		assert.NilError(t, err)
	})
}

func testDalecTargetArg(ctx context.Context, t *testing.T, testCfg targetConfig) {
	t.Parallel()
	ctx = startTestSpan(ctx, t)

	spec := newSimpleSpec()
	if spec.Args == nil {
		spec.Args = make(map[string]string)
	}
	spec.Args["DALEC_TARGET"] = ""
	if spec.Build.Env == nil {
		spec.Build.Env = make(map[string]string)
	}
	spec.Build.Env["DALEC_TARGET"] = "$DALEC_TARGET"

	expect, _, _ := strings.Cut(testCfg.Package, "/")
	spec.Build.Steps = []dalec.BuildStep{
		{Command: fmt.Sprintf("[ \"$DALEC_TARGET\" = %q ]", expect)},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		solveT(ctx, t, client, newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(testCfg.Package)))
	})
}

func testMixGlobalTargetDependencies(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("global target dependencies", func(t *testing.T) {
		distro := strings.Split(cfg.Target.Container, "/")[0]
		spec := newSimpleSpec()
		spec.Dependencies = &dalec.PackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"curl": {},
			},
		}

		spec.Targets = map[string]dalec.Target{
			distro: {
				Dependencies: &dalec.PackageDependencies{
					Build: map[string]dalec.PackageConstraints{
						"golang": {},
					},
				},
			},
		}

		// Spec had target specific build dependency of golang,
		// but the global runtime dependency of curl should still be installed
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "Check that dependencies are installed",
				Files: map[string]dalec.FileCheckOutput{
					"/usr/bin/curl": {
						Permissions: 0o755,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			solveT(ctx, t, client, newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package)))
		})
	})
}

func testDisableStrip(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	skip.If(t, cfg.SkipStripTest, "skipping test as it is not supported for this target: "+cfg.Target.Container)

	newSpec := func() *dalec.Spec {
		spec := newSimpleSpec()
		spec.Args = map[string]string{
			"TARGETARCH": "",
		}

		spec.Sources = map[string]dalec.Source{
			"src": {
				Generate: []*dalec.SourceGenerator{
					{
						Gomod: &dalec.GeneratorGomod{},
					},
				},
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							"main.go": {Contents: gomodFixtureMain},
							"go.mod":  {Contents: gomodFixtureMod},
							"go.sum":  {Contents: gomodFixtureSum},
						},
					},
				},
			},
		}

		spec.Dependencies = &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				cfg.GetPackage("golang"): {},
			},
		}
		spec.Artifacts = dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"bad-executable": {},
			},
			Libs: map[string]dalec.ArtifactConfig{
				"bad-executable": {},
			},
		}

		spec.Build.Env = map[string]string{
			"TARGETARCH": "$TARGETARCH",
		}

		spec.Build.Steps = []dalec.BuildStep{
			// Build a binary for a different architecture
			// This should make `strip` fail.
			//
			// Note: The test is specifically using ppc64le as GOARCH
			// because it seems alma/rockylinux do not error ons trip except for ppc64le.
			// Even this is a stretch as that does not even work as expected at version < v9.
			{
				Command: `cd src; if [ "${TARGETARCH}" = "ppc64le" ]; then export GOARCH=amd64; else export GOARCH=ppc64le; fi; go build -o ../bad-executable main.go`,
			},
		}
		return spec
	}

	t.Run("strip enabled", func(t *testing.T) {
		// Make sure that we get a build failure when strip is enabled
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			spec := newSpec()
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))

			_, err := client.Solve(ctx, req)
			assert.ErrorType(t, pkgerrors.Cause(err), &moby_buildkit_v1_frontend.ExitError{})
		})
	})

	t.Run("strip disabled", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			spec := newSpec()
			spec.Artifacts.DisableStrip = true

			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			solveT(ctx, t, client, req)
		})
	})
}

func testTargetPlatform(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	ctx = startTestSpan(ctx, t)

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		bp := readDefaultPlatform(ctx, t, client)

		var tp ocispecs.Platform
		for _, p := range cfg.Platforms {
			if platforms.OnlyStrict(bp).Match(p) {
				continue
			}

			tp = p

			if bp.Architecture != "arm64" {
				break
			}
			// On arm64, let's try and build for armv7 to help speed up tests
			// Also makes sure that building arm on arm64 gives the correct result since this can run natively (usually)
			if tp.Architecture == "arm" {
				break
			}
		}

		skip.If(t, tp.OS == "", "No other platforms available to test")
		assert.Assert(t, tp.Architecture != bp.Architecture, "Target and build arches are the same")

		spec := newSimpleSpec()
		spec.Args = map[string]string{
			"TARGETARCH": "",
		}

		spec.Build.Env = map[string]string{
			"TARGETARCH": "$TARGETARCH",
		}

		spec.Build.Steps = []dalec.BuildStep{
			{
				Command: fmt.Sprintf(`[ "${TARGETARCH}" = "%s" ]`, tp.Architecture),
			},
		}

		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package), withPlatform(tp))
		res := solveT(ctx, t, client, sr)
		ref, err := res.SingleRef()
		assert.NilError(t, err)

		_, err = ref.StatFile(ctx, gwclient.StatRequest{
			Path: cfg.PackageOutputPath(spec, tp),
		})
		assert.NilError(t, err)

		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container), withPlatform(tp))
		res = solveT(ctx, t, client, sr)
		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		assert.Assert(t, ok, "missing image config in result metadata")

		var img dalec.DockerImageSpec
		err = json.Unmarshal(dt, &img)
		assert.NilError(t, err)
		assert.Assert(t, platforms.OnlyStrict(tp).Match(img.Platform), "platform mismatch, expected %v, got %v", tp, img.Platform)
	})
}

func extractDebControlFile(t *testing.T, f io.ReaderAt) io.ReadCloser {
	t.Helper()

	ar, err := deb.LoadAr(f)
	assert.NilError(t, err)

	for {
		entry, err := ar.Next()
		if err == io.EOF {
			break
		}
		assert.NilError(t, err)

		if entry == nil {
			break
		}

		if !strings.HasPrefix(entry.Name, "control.") {
			continue
		}

		rdr, err := compression.DecompressStream(entry.Data)
		assert.NilError(t, err)
		return rdr
	}
	return nil
}

type mapEnvGetter map[string]string

func (m mapEnvGetter) Get(key string) (string, bool) {
	v, ok := m[key]
	return v, ok
}

func (m mapEnvGetter) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func testPackageProvidesReplaces(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	ctx = startTestSpan(ctx, t)

	spec := newSimpleSpec()
	spec.Args = map[string]string{
		"SOME_VER": "1.0.0",
	}
	spec.Provides = map[string]dalec.PackageConstraints{
		"other-package1": {},
		"other-package2": {
			Version: []string{"= ${SOME_VER}"},
		},
	}

	spec.Replaces = map[string]dalec.PackageConstraints{
		"other-package1": {},
		"other-package2": {
			Version: []string{"= ${SOME_VER}"},
		},
	}

	envGetter := mapEnvGetter(spec.Args)

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
		res := solveT(ctx, t, client, sr)

		ref, err := res.SingleRef()
		assert.NilError(t, err)

		pkgfs := bkfs.FromRef(ctx, ref)

		checkRPM := func(path string) {
			// Check that the package provides are in the rpm
			f, err := pkgfs.Open(path)
			assert.NilError(t, err)
			defer f.Close()

			pkg, err := rpm.Read(f)
			if err != nil {
				assert.NilError(t, err)
			}

			var found int
			lex := shell.NewLex('\\')
			for _, p := range pkg.Provides() {
				if p.Name() == spec.Name || strings.HasPrefix(p.Name(), spec.Name+"(") {
					continue
				}

				compare, ok := spec.Provides[p.Name()]
				assert.Assert(t, ok, p.Name())
				found++

				if len(compare.Version) > 0 {
					v := strings.TrimPrefix(compare.Version[0], "= ")
					res, err := lex.ProcessWordWithMatches(v, envGetter)
					assert.NilError(t, err)
					assert.Equal(t, p.Version(), res.Result, "version mismatch for %s", p.Name())
				}
			}

			found = 0
			for _, r := range pkg.Obsoletes() {
				compare, ok := spec.Provides[r.Name()]
				assert.Assert(t, ok, r.Name())
				found++

				if len(compare.Version) > 0 {
					v := strings.TrimPrefix(compare.Version[0], "= ")
					res, err := lex.ProcessWordWithMatches(v, envGetter)
					assert.NilError(t, err)
					assert.Equal(t, r.Version(), res.Result, "version mismatch for %s", r.Name())
				}
			}
			assert.Equal(t, found, len(spec.Provides), "not all provides found in rpm")
		}

		checkDeb := func(path string) {
			f, err := pkgfs.Open(path)
			assert.NilError(t, err)
			defer f.Close()

			cf := extractDebControlFile(t, f.(io.ReaderAt))
			assert.Assert(t, cf != nil, "control file not found in deb")
			defer cf.Close()

			scanner := bufio.NewScanner(cf)

			expect := "other-package1, other-package2 (= 1.0.0)"

			var found int
			for scanner.Scan() {
				txt := scanner.Text()

				key, value, ok := strings.Cut(txt, ": ")
				if !ok {
					continue
				}

				switch key {
				case "Replaces", "Provides":
					found++
					assert.Equal(t, value, expect, key+" mismatch")
				default:
				}

				if found == 2 {
					break
				}
			}
			assert.NilError(t, err)
			assert.Equal(t, found, 2, "missing either provides or replaces in deb")
		}

		var found bool
		err = fs.WalkDir(pkgfs, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			if strings.HasSuffix(path, ".rpm") && !strings.HasSuffix(path, ".src.rpm") {
				checkRPM(path)
				found = true
				return nil
			}
			if strings.HasSuffix(path, ".deb") {
				found = true
				checkDeb(path)
			}
			return nil
		})
		assert.NilError(t, err)
		assert.Assert(t, found, "no rpm or deb found in package")
	})
}

func testDisableAutoRequire(ctx context.Context, t *testing.T, cfg targetConfig) {
	var zlibDep string
	switch {
	case strings.HasSuffix(cfg.Package, "rpm"):
		zlibDep = "zlib-devel"
	case strings.HasSuffix(cfg.Package, "deb"):
		zlibDep = "zlib1g-dev"
	default:
		t.Fatalf("unsupported package type: %s", cfg.Package)
	}

	newSpec := func() *dalec.Spec {
		spec := newSimpleSpec()
		spec.Artifacts = dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"test": {},
			},
		}

		spec.Dependencies = &dalec.PackageDependencies{
			Build: map[string]dalec.PackageConstraints{
				"gcc":   {},
				zlibDep: {},
			},
		}

		spec.Build.Steps = []dalec.BuildStep{
			{
				Command: "gcc -o test main.c -lz",
			},
		}

		spec.Sources = map[string]dalec.Source{
			"main.c": {
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: `
#include <zlib.h>
#include <stdio.h>

int main() {
    printf("zlib version: %s\n", zlibVersion());
    return 0;
}
`,
						Permissions: 0o755,
					},
				},
			},
		}
		return spec
	}

	checkRPM := func(ctx context.Context, t *testing.T, spec *dalec.Spec) {
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
			res := solveT(ctx, t, client, sr)

			ref, err := res.SingleRef()
			assert.NilError(t, err)

			pkgfs := bkfs.FromRef(ctx, ref)

			var found bool
			err = fs.WalkDir(pkgfs, ".", func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}

				if !strings.HasSuffix(path, ".rpm") {
					return nil
				}

				if strings.HasSuffix(path, ".src.rpm") {
					return nil
				}

				found = true
				f, err := pkgfs.Open(path)
				assert.NilError(t, err)
				defer f.Close()

				pkg, err := rpm.Read(f)
				assert.NilError(t, err)

				var found bool
				for _, r := range pkg.Requires() {
					if strings.Contains(r.Name(), "zlib") || strings.Contains(r.Name(), "libz") {
						found = true
						break
					}
				}

				if spec.Artifacts.DisableAutoRequires {
					assert.Check(t, !found, "auto-requires found %v", pkg.Requires())
				} else {
					assert.Check(t, found, "auto-requires not found %v", pkg.Requires())
				}

				return fs.SkipAll
			})
			assert.NilError(t, err)
			assert.Assert(t, found)
		})
	}

	checkDeb := func(ctx context.Context, t *testing.T, spec *dalec.Spec) {
		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Package))
			res := solveT(ctx, t, client, sr)
			ref, err := res.SingleRef()
			assert.NilError(t, err)

			pkgfs := bkfs.FromRef(ctx, ref)

			var found bool
			err = fs.WalkDir(pkgfs, ".", func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}

				if !strings.HasSuffix(path, ".deb") {
					return nil
				}
				if strings.Contains(path, "dbg") {
					// skip debug packages
					return nil
				}

				found = true
				f, err := pkgfs.Open(path)
				if err != nil {
					return err
				}
				defer f.Close()

				cf := extractDebControlFile(t, f.(io.ReaderAt))
				defer cf.Close()

				buf := bytes.NewBuffer(nil)
				scanner := bufio.NewScanner(io.TeeReader(cf, buf))
				var found bool
				for scanner.Scan() {
					txt := scanner.Text()
					key, value, ok := strings.Cut(txt, ": ")
					if !ok {
						continue
					}
					if key != "Depends" {
						continue
					}

					if strings.Contains(value, "zlib") {
						found = true
						break
					}
				}

				if spec.Artifacts.DisableAutoRequires {
					assert.Check(t, !found, "auto-requires found: %s\n%s", path, buf)
				} else {
					assert.Check(t, found, "auto-requires not found: %s \n%s", path, buf)
				}
				return fs.SkipAll
			})

			assert.NilError(t, err)
			assert.Assert(t, found, "no deb found in package")
		})
	}

	check := func(ctx context.Context, t *testing.T, spec *dalec.Spec) {
		switch {
		case strings.HasSuffix(cfg.Package, "rpm"):
			checkRPM(ctx, t, spec)
		case strings.HasSuffix(cfg.Package, "deb"):
			checkDeb(ctx, t, spec)
		default:
			t.Fatalf("unsupported package type: %s", cfg.Package)
		}
	}

	// Test makes sure that when `DisableAutoRequires` is set to false that those requirements are added
	// This ensures that that the actual test where `DisableAutoRequires` is set to true is valid.
	t.Run("disable-auto-requires=false", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		check(ctx, t, newSpec())
	})

	t.Run("disable-auto-requires=true", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := newSpec()
		spec.Artifacts.DisableAutoRequires = true
		check(ctx, t, spec)
	})
}

func testPrebuiltPackages(ctx context.Context, t *testing.T, testConfig testLinuxConfig) {
	t.Run("Use pre-built packages from build context", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		preBuiltSpec := &dalec.Spec{
			Name:        "test-prebuilt-package",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Test using pre-built packages",
			Sources: map[string]dalec.Source{
				"hello": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/bin/sh\necho 'Hello from pre-built package'",
							Permissions: 0o755,
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"hello": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Test that binary from pre-built package works",
					Steps: []dalec.TestStep{
						{
							Command: "/usr/bin/hello",
							Stdout: dalec.CheckOutput{
								Contains: []string{"Hello from pre-built package"},
							},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			// Build the package which does not contain the unique marker file.
			pkgSr := newSolveRequest(withSpec(ctx, t, preBuiltSpec), withBuildTarget(testConfig.Target.Package))
			pkgRes := solveT(ctx, t, client, pkgSr)
			pkgRef, err := pkgRes.SingleRef()
			assert.NilError(t, err)
			pkgSt, _ := pkgRef.ToState()

			// Update the spec to include a unique marker file.
			//
			// If the marker file is present in the later container check,
			// it means the pre-built package was not used and was rebuilt
			// with this updated spec.
			preBuiltSpec.Artifacts.DataDirs = map[string]dalec.ArtifactConfig{
				"/etc/marker.txt": {},
			}

			// Build the container and pass the pre-built package as a dependency.
			containerSr := newSolveRequest(
				withSpec(ctx, t, preBuiltSpec),
				withBuildTarget(testConfig.Target.Container),
				withBuildContext(ctx, t, dalec.GenericPkg, pkgSt),
			)
			containerRes := solveT(ctx, t, client, containerSr)
			containerRef, err := containerRes.SingleRef()
			assert.NilError(t, err)

			// Read the contents of the package to ensure it does not have the marker file.
			contents, err := containerRef.ReadFile(ctx, gwclient.ReadRequest{
				Filename: "/etc/marker.txt",
			})
			// The marker file should not be present in the container,
			// as it was not part of the pre-built package.
			assert.Assert(t, contents == nil, "marker file should not be present in the container")
			assert.ErrorContains(t, err, "open /etc/marker.txt: no such file or directory")
		})
	})
}

func testGomodEdits(_ context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("require directive", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-gomod-require",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Test gomod require directive",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{
								Edits: &dalec.GomodEdits{
									Require: []dalec.GomodRequire{
										// Upgrade blackfriday to a specific version
										{Module: "github.com/russross/blackfriday/v2", Version: "github.com/russross/blackfriday/v2@v2.1.0"},
									},
								},
							},
						},
					},
					Git: &dalec.SourceGit{
						URL:    "https://github.com/cpuguy83/go-md2man.git",
						Commit: "v2.0.0",
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					"golang": {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "cd src && go build -o /tmp/go-md2man .",
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"/tmp/go-md2man": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("replace directive", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-gomod-replace",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Test gomod replace directive",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{
								Edits: &dalec.GomodEdits{
									Replace: []dalec.GomodReplace{
										// Replace with a specific version
										{Old: "github.com/russross/blackfriday/v2", New: "github.com/russross/blackfriday/v2@v2.1.0"},
									},
								},
							},
						},
					},
					Git: &dalec.SourceGit{
						URL:    "https://github.com/cpuguy83/go-md2man.git",
						Commit: "v2.0.0",
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					"golang": {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "cd src && go build -o /tmp/go-md2man .",
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"/tmp/go-md2man": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("combined require and replace", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-gomod-combined",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/project-dalec/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Test gomod combined require and replace directives",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{
								Edits: &dalec.GomodEdits{
									Require: []dalec.GomodRequire{
										{Module: "github.com/stretchr/testify", Version: "github.com/stretchr/testify@v1.8.4"},
									},
									Replace: []dalec.GomodReplace{
										{Old: "github.com/russross/blackfriday/v2", New: "github.com/russross/blackfriday/v2@v2.1.0"},
									},
								},
							},
						},
					},
					Git: &dalec.SourceGit{
						URL:    "https://github.com/cpuguy83/go-md2man.git",
						Commit: "v2.0.0",
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					"golang": {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "cd src && go build -o /tmp/go-md2man .",
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"/tmp/go-md2man": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("multi-module with edits", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-gomod-multi-module",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "Apache-2.0",
			Website:     "https://github.com/kubernetes-sigs/kustomize",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Test gomod edits with multi-module repo",
			Sources: map[string]dalec.Source{
				"kustomize": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{
								Paths: []string{"kustomize", "kyaml", "api", "cmd/config"},
								Edits: &dalec.GomodEdits{
									Require: []dalec.GomodRequire{
										{Module: "github.com/spf13/cobra", Version: "github.com/spf13/cobra@v1.8.0"},
									},
								},
							},
						},
					},
					Git: &dalec.SourceGit{
						URL:    "https://github.com/kubernetes-sigs/kustomize.git",
						Commit: "kustomize/v5.4.3",
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					"golang": {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "cd kustomize/kustomize && go build -o /tmp/kustomize .",
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"/tmp/kustomize": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			solveT(ctx, t, client, req)
		})
	})
}
