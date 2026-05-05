package specgen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectRepo_ForcedTypeStillEnrichesPackageJSON(t *testing.T) {
	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "@example/hello-cli",
  "version": "1.2.3",
  "license": "MIT",
  "description": "Hello CLI",
  "homepage": "https://example.com/hello",
  "scripts": { "build": "tsc" },
  "main": "dist/index.js",
  "bin": { "hello": "bin/hello.js" }
}`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	f, warnings, err := DetectRepo(dir, "node")
	if err != nil {
		t.Fatal(err)
	}
	_ = warnings

	if f.PrimaryType != "node" {
		t.Fatalf("expected forced node type, got %q", f.PrimaryType)
	}
	if f.Name != "example-hello-cli" {
		t.Fatalf("unexpected name: %q", f.Name)
	}
	if f.Version != "1.2.3" {
		t.Fatalf("unexpected version: %q", f.Version)
	}
	if f.License != "MIT" {
		t.Fatalf("unexpected license: %q", f.License)
	}
	if f.Description != "Hello CLI" {
		t.Fatalf("unexpected description: %q", f.Description)
	}
	if f.Website != "https://example.com/hello" {
		t.Fatalf("unexpected website: %q", f.Website)
	}
	if !f.NodeHasBuild {
		t.Fatalf("expected NodeHasBuild=true")
	}
	if f.NodeMain != "dist/index.js" {
		t.Fatalf("unexpected NodeMain: %q", f.NodeMain)
	}
	if f.NodeBinName != "hello" {
		t.Fatalf("unexpected NodeBinName: %q", f.NodeBinName)
	}
	if !f.SuggestedSourceGeneratorSafe || f.SuggestedSourceGenerator != "node-mod" {
		t.Fatalf("expected safe node source generator, got generator=%q safe=%v reason=%q", f.SuggestedSourceGenerator, f.SuggestedSourceGeneratorSafe, f.SuggestedSourceGeneratorReason)
	}
}

func TestDetectRepo_GoSourceGeneratorSafety_SafeDirective(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := DetectRepo(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.SuggestedSourceGenerator != "gomod" {
		t.Fatalf("expected gomod suggestion, got %q", f.SuggestedSourceGenerator)
	}
	if !f.SuggestedSourceGeneratorSafe {
		t.Fatalf("expected gomod suggestion to be safe, reason=%q", f.SuggestedSourceGeneratorReason)
	}
}

func TestDetectRepo_GoSourceGeneratorSafety_UnsafeDirective(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/foo\ngo 1.25.0\ntoolchain go1.25.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f, _, err := DetectRepo(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.SuggestedSourceGenerator != "gomod" {
		t.Fatalf("expected gomod suggestion, got %q", f.SuggestedSourceGenerator)
	}
	if f.SuggestedSourceGeneratorSafe {
		t.Fatalf("expected gomod suggestion to be unsafe")
	}
	if f.SuggestedSourceGeneratorReason == "" {
		t.Fatalf("expected unsafe reason to be populated")
	}
}
