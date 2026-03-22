package specgen

import (
	"context"
	"os"
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
	if res.Plan == nil || res.Plan.Intent != IntentPackageContainer {
		t.Fatalf("expected package+container plan, got %+v", res.Plan)
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
