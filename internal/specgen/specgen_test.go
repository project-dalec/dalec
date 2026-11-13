package specgen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate_GoRepo_EmitsGomodGeneratorWhenSafe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.YAML)
	if !strings.Contains(s, "# syntax=ghcr.io/project-dalec/dalec/frontend:latest") {
		t.Fatalf("missing syntax header:\n%s", s)
	}
	if !strings.Contains(s, "gomod") {
		t.Fatalf("expected gomod generator for safe go.mod:\n%s", s)
	}
	if !strings.Contains(s, "golang") {
		t.Fatalf("expected golang build dep:\n%s", s)
	}
	if !strings.Contains(s, "go build") {
		t.Fatalf("expected go build step:\n%s", s)
	}
	if res.Plan == nil || res.Plan.Intent != IntentPackage {
		t.Fatalf("expected package plan, got %+v", res.Plan)
	}
}

func TestGenerate_GoRepo_SkipsUnsafeGomodGenerator(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.25.0\ntool (\n\tstringer\n)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.YAML)
	if strings.Contains(s, "gomod") {
		t.Fatalf("did not expect gomod generator for unsafe go.mod:\n%s", s)
	}
	if !strings.Contains(s, "go build") {
		t.Fatalf("expected direct go build step:\n%s", s)
	}
	if !strings.Contains(s, "golang") {
		t.Fatalf("expected golang build dep:\n%s", s)
	}
}

func TestGenerate_NodeRepo_UsesNodeAdapter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "hello-node",
  "version": "1.2.3",
  "license": "MIT",
  "description": "Hello Node",
  "main": "dist/index.js",
  "scripts": {"build": "node build.js"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.YAML)
	if !strings.Contains(s, "nodejs") {
		t.Fatalf("expected nodejs build dep:\n%s", s)
	}
	if !strings.Contains(s, "npm") {
		t.Fatalf("expected npm-related build path:\n%s", s)
	}
	if !strings.Contains(s, "npm run build") {
		t.Fatalf("expected node build step:\n%s", s)
	}
	if res.Plan == nil || !strings.HasPrefix(res.Plan.BuildStyle, "node") {
		t.Fatalf("expected node build style, got %+v", res.Plan)
	}
}

func TestGenerate_GitSourcePreferredWhenMetadataAvailable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("remote", "add", "origin", "https://github.com/example/foo.git")
	run("add", ".")
	run("commit", "-m", "init")

	res, err := Generate(context.Background(), Options{
		RepoDir:         dir,
		SyntaxImage:     "ghcr.io/project-dalec/dalec/frontend:latest",
		PreferGitSource: true,
		SourceMode:      "context",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(res.YAML)
	if !strings.Contains(s, "git:") {
		t.Fatalf("expected git source in YAML:\n%s", s)
	}
	if !strings.Contains(s, "https://github.com/example/foo") {
		t.Fatalf("expected git remote in YAML:\n%s", s)
	}
}

func TestGenerate_GoLibraryRepo_FallsBackWithoutBinaryArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/libfoo\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package libfoo\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Spec == nil {
		t.Fatal("expected spec")
	}
}

func TestGenerate_MixedRustNodeWrapperRepo_PrefersRustBaseline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"sentry-cli\"\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "sentry-cli",
  "version": "1.0.0",
  "bin": {"sentry-cli": "bin/sentry-cli.js"},
  "scripts": {"build": "cargo build --release"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "sentry-cli.js"), []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DetectedType != "rust" {
		t.Fatalf("expected rust detected type, got %q", res.DetectedType)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if !strings.HasPrefix(res.Plan.BuildStyle, "rust") {
		t.Fatalf("expected rust build style, got %q", res.Plan.BuildStyle)
	}
	if res.Plan.Intent == IntentWindowsCross {
		t.Fatalf("did not expect windowscross intent for mixed rust/node wrapper repo")
	}
	if !strings.Contains(string(res.YAML), "cargo build") {
		t.Fatalf("expected cargo build in emitted YAML:\n%s", string(res.YAML))
	}
}

func TestGenerate_WindowsMentionsAloneDoNotForceWindowsCross(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"uv\"\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("Install on Windows using uv.exe if you want, but this repo is not a dedicated cross-compile package.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.Intent == IntentWindowsCross {
		t.Fatalf("did not expect windowscross intent from weak windows mentions")
	}
}

func TestGenerate_ExplicitWindowsCrossSignals_SelectWindowsCross(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/winctl\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "winctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "winctl", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("build-windows:\n\tGOOS=windows GOARCH=amd64 go build -o bin/winctl.exe ./cmd/winctl\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.Intent != IntentWindowsCross {
		t.Fatalf("expected windowscross intent, got %q", res.Plan.Intent)
	}
	if res.Plan.TargetFamily != TargetFamilyWindows {
		t.Fatalf("expected windows target family, got %q", res.Plan.TargetFamily)
	}
}

func TestGenerate_RustCliRepo_DefaultsToPackageIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"uv\"\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.Intent != IntentPackage {
		t.Fatalf("expected package intent for rust cli baseline, got %q", res.Plan.Intent)
	}
}

func TestGenerate_RustCliRepo_WithDockerfile_StillDefaultsToPackageIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"uv\"\nversion = \"1.0.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:3.20\nCOPY . /src\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.Intent != IntentPackage {
		t.Fatalf("expected package intent for rust cli repo with Dockerfile, got %q", res.Plan.Intent)
	}
}

func TestGenerate_NodeHomepagePrefersRepositoryOverInstallScript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "ruff",
  "version": "1.0.0",
  "homepage": "https://astral.sh/ruff/install.sh",
  "repository": {"type": "git", "url": "https://github.com/astral-sh/ruff.git"},
  "bin": {"ruff": "bin/ruff.js"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Spec == nil {
		t.Fatal("expected spec")
	}
	if res.Spec.Website != "https://github.com/astral-sh/ruff" {
		t.Fatalf("expected repository URL to win over install script homepage, got %q", res.Spec.Website)
	}
}

func TestGenerate_GoWindowsHintsRequireMultipleSignals(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/nerdctl\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "nerdctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "nerdctl", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("Supports Windows too.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.Intent == IntentWindowsCross {
		t.Fatalf("did not expect windowscross intent from weak Go windows hints")
	}
}

func TestDefaultConfigInstallPath_NormalizesEtcPrefix(t *testing.T) {
	if got := defaultConfigInstallPath("contrib/Dockerfile.test.d/cri-in-userns/etc_containerd_config.toml"); got != "/etc/containerd/config.toml" {
		t.Fatalf("unexpected normalized config path: %q", got)
	}
}

func TestBinaryOutputPathFromArtifact_DoesNotDoubleExe(t *testing.T) {
	art := PlannedArtifact{Kind: "binary", Path: "src/bin/nerdctl.exe", Name: "nerdctl"}
	if got := binaryOutputPathFromArtifact(art, "nerdctl.exe"); got != "bin/nerdctl.exe" {
		t.Fatalf("unexpected binary output path: %q", got)
	}
}

func TestGenerate_GoMultiPlatformMatrix_DoesNotForceWindowsCross(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/nerdctl\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "nerdctl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "nerdctl", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("lint:\n\tGOOS=linux make lint-go \\\n\t&& GOOS=windows make lint-go \\\n\t&& GOOS=darwin make lint-go\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.Intent == IntentWindowsCross {
		t.Fatalf("did not expect windowscross intent from multi-platform GOOS matrix")
	}
}

func TestGenerate_CargoRepositoryBeatsInstallScriptHomepage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"uv\"\nversion = \"1.0.0\"\nhomepage = \"https://astral.sh/uv/install.sh\"\nrepository = \"https://github.com/astral-sh/uv\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{RepoDir: dir, SyntaxImage: "ghcr.io/project-dalec/dalec/frontend:latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Spec == nil {
		t.Fatal("expected spec")
	}
	if res.Spec.Website != "https://github.com/astral-sh/uv" {
		t.Fatalf("expected repository URL to beat install script homepage, got %q", res.Spec.Website)
	}
}

func TestGenerate_UserIntentOverridesComponentPackageAndBuildTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/keda\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"adapter", "operator", "webhooks"} {
		if err := os.MkdirAll(filepath.Join(dir, "cmd", name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "cmd", name, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res, err := Generate(context.Background(), Options{
		RepoDir:       dir,
		SyntaxImage:   "ghcr.io/project-dalec/dalec/frontend:latest",
		MainComponent: "operator",
		PackageName:   "keda-operator",
		BinaryName:    "keda-operator",
		BuildTarget:   "./cmd/operator",
		BuildStyle:    "go-simple",
		Entrypoint:    "keda-operator",
		Command:       "--help",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil {
		t.Fatal("expected plan")
	}
	if res.Plan.MainComponent != "operator" {
		t.Fatalf("expected operator main component, got %q", res.Plan.MainComponent)
	}
	if res.Plan.PackageName != "keda-operator" {
		t.Fatalf("expected keda-operator package name, got %q", res.Plan.PackageName)
	}
	if res.Plan.PrimaryBinaryName != "keda-operator" {
		t.Fatalf("expected keda-operator primary binary name, got %q", res.Plan.PrimaryBinaryName)
	}
	if res.Plan.PrimaryBuildTarget != "./cmd/operator" {
		t.Fatalf("expected explicit build target, got %q", res.Plan.PrimaryBuildTarget)
	}
	s := string(res.YAML)
	if !strings.Contains(s, "go build -trimpath -o bin/keda-operator ./cmd/operator") {
		t.Fatalf("expected explicit build target and binary name in YAML:\n%s", s)
	}
	if !strings.Contains(s, "/usr/bin/keda-operator") {
		t.Fatalf("expected tests/artifacts to use explicit binary name:\n%s", s)
	}
}

func TestGenerate_PlanRecordsUserIntent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "foo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "foo", "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Generate(context.Background(), Options{
		RepoDir:       dir,
		SyntaxImage:   "ghcr.io/project-dalec/dalec/frontend:latest",
		MainComponent: "foo",
		PackageName:   "fooctl",
		BuildStyle:    "go-simple",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Plan == nil || res.Plan.UserIntent == nil {
		t.Fatal("expected user intent to be recorded in the plan")
	}
	fields := strings.Join(res.Plan.UserIntent.ExplicitFields, ",")
	if !strings.Contains(fields, "main_component") {
		t.Fatalf("expected main_component in explicit fields, got %#v", res.Plan.UserIntent.ExplicitFields)
	}
	if !strings.Contains(fields, "package_name") {
		t.Fatalf("expected package_name in explicit fields, got %#v", res.Plan.UserIntent.ExplicitFields)
	}
	if !strings.Contains(fields, "build_style") {
		t.Fatalf("expected build_style in explicit fields, got %#v", res.Plan.UserIntent.ExplicitFields)
	}
}
