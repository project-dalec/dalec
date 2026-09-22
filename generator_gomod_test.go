package dalec

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/patternmatcher"
)

func TestGomodReplaceUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		unmarshal   func([]byte, interface{}) error
		expectErr   bool
		expectedOld string
		expectedNew string
	}{
		{
			name:        "YAML string format",
			input:       `"github.com/stretchr/testify => github.com/stretchr/testify@v1.8.0"`,
			unmarshal:   yaml.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/stretchr/testify",
			expectedNew: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "YAML struct format",
			input: `
old: github.com/cpuguy83/tar2go
new: github.com/cpuguy83/tar2go@v0.3.1
`,
			unmarshal:   yaml.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/cpuguy83/tar2go",
			expectedNew: "github.com/cpuguy83/tar2go@v0.3.1",
		},
		{
			name:        "JSON string format",
			input:       `"github.com/stretchr/testify => github.com/stretchr/testify@v1.8.0"`,
			unmarshal:   json.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/stretchr/testify",
			expectedNew: "github.com/stretchr/testify@v1.8.0",
		},
		{
			name:        "JSON struct format",
			input:       `{"old":"github.com/cpuguy83/tar2go","new":"github.com/cpuguy83/tar2go@v0.3.1"}`,
			unmarshal:   json.Unmarshal,
			expectErr:   false,
			expectedOld: "github.com/cpuguy83/tar2go",
			expectedNew: "github.com/cpuguy83/tar2go@v0.3.1",
		},
		{
			name:      "invalid string format - no arrow",
			input:     `"github.com/stretchr/testify"`,
			unmarshal: yaml.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid struct format - missing new",
			input:     `{"old":"github.com/cpuguy83/tar2go"}`,
			unmarshal: json.Unmarshal,
			expectErr: true,
		},
		{
			name:      "invalid struct format - empty old",
			input:     `{"old":"","new":"github.com/cpuguy83/tar2go@v0.3.1"}`,
			unmarshal: json.Unmarshal,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var repl GomodReplace
			err := tt.unmarshal([]byte(tt.input), &repl)

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if repl.Original != tt.expectedOld {
				t.Errorf("expected Old=%q, got %q", tt.expectedOld, repl.Original)
			}
			if repl.Update != tt.expectedNew {
				t.Errorf("expected New=%q, got %q", tt.expectedNew, repl.Update)
			}
		})
	}
}

func TestGomodReplaceGoModEditArg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		repl        GomodReplace
		expectErr   bool
		expectedArg string
	}{
		{
			name: "valid replace",
			repl: GomodReplace{
				Original: "github.com/stretchr/testify",
				Update:   "github.com/stretchr/testify@v1.8.0",
			},
			expectErr:   false,
			expectedArg: "github.com/stretchr/testify=github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "adds incompatible for v2+ module path without major suffix",
			repl: GomodReplace{
				Original: "github.com/docker/cli",
				Update:   "github.com/docker/cli@v29.2.0",
			},
			expectErr:   false,
			expectedArg: "github.com/docker/cli=github.com/docker/cli@v29.2.0+incompatible",
		},
		{
			name: "keeps existing incompatible suffix",
			repl: GomodReplace{
				Original: "github.com/docker/cli",
				Update:   "github.com/docker/cli@v29.2.0+incompatible",
			},
			expectErr:   false,
			expectedArg: "github.com/docker/cli=github.com/docker/cli@v29.2.0+incompatible",
		},
		{
			name: "does not add incompatible for proper /v2 path",
			repl: GomodReplace{
				Original: "example.com/mod/v2",
				Update:   "example.com/mod/v2@v2.3.4",
			},
			expectErr:   false,
			expectedArg: "example.com/mod/v2=example.com/mod/v2@v2.3.4",
		},
		{
			name: "does not add incompatible for v1",
			repl: GomodReplace{
				Original: "github.com/stretchr/testify",
				Update:   "github.com/stretchr/testify@v1.8.0",
			},
			expectErr:   false,
			expectedArg: "github.com/stretchr/testify=github.com/stretchr/testify@v1.8.0",
		},
		{
			name: "does not rewrite local path replacements",
			repl: GomodReplace{
				Original: "github.com/docker/cli",
				Update:   "../docker-cli",
			},
			expectErr:   false,
			expectedArg: "github.com/docker/cli=../docker-cli",
		},
		{
			name: "does not add incompatible when other build metadata is present",
			repl: GomodReplace{
				Original: "example.com/mod",
				Update:   "example.com/mod@v2.0.0+meta",
			},
			expectErr:   false,
			expectedArg: "example.com/mod=example.com/mod@v2.0.0+meta",
		},
		{
			name: "empty old",
			repl: GomodReplace{
				Original: "",
				Update:   "github.com/stretchr/testify@v1.8.0",
			},
			expectErr: true,
		},
		{
			name: "empty new",
			repl: GomodReplace{
				Original: "github.com/stretchr/testify",
				Update:   "",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arg, err := tt.repl.goModEditArg()

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if arg != tt.expectedArg {
				t.Errorf("expected %q, got %q", tt.expectedArg, arg)
			}
		})
	}
}

func TestGomodEditArgs_DropRequire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		gen          *GeneratorGomod
		expectErr    bool
		expectedArgs string
	}{
		{
			name: "drop converts to droprequire",
			gen: &GeneratorGomod{
				Edits: &GomodEdits{
					Drop: []string{"example.com/old-submodule"},
				},
			},
			expectedArgs: "-droprequire=example.com/old-submodule",
		},
		{
			name: "multiple drops",
			gen: &GeneratorGomod{
				Edits: &GomodEdits{
					Drop: []string{
						"example.com/mod-a",
						"example.com/mod-b",
					},
				},
			},
			expectedArgs: "-droprequire=example.com/mod-a\n-droprequire=example.com/mod-b",
		},
		{
			name: "mixed drop and replace",
			gen: &GeneratorGomod{
				Edits: &GomodEdits{
					Replace: []GomodReplace{
						{Original: "example.com/grpc", Update: "example.com/grpc@v1.79.3"},
					},
					Drop: []string{"example.com/grpc/stats/otel"},
				},
			},
			expectedArgs: "-replace=example.com/grpc=example.com/grpc@v1.79.3\n-droprequire=example.com/grpc/stats/otel",
		},
		{
			name: "drop with empty module path errors",
			gen: &GeneratorGomod{
				Edits: &GomodEdits{
					Drop: []string{""},
				},
			},
			expectErr: true,
		},
		{
			name: "drop with invalid module path errors",
			gen: &GeneratorGomod{
				Edits: &GomodEdits{
					Drop: []string{"INVALID PATH"},
				},
			},
			expectErr: true,
		},
		{
			name:         "nil edits returns empty",
			gen:          &GeneratorGomod{},
			expectedArgs: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := gomodEditArgs(tt.gen)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if args != tt.expectedArgs {
				t.Errorf("expected %q, got %q", tt.expectedArgs, args)
			}
		})
	}
}

func TestGomodDepsExcludesModuleZipCache(t *testing.T) {
	t.Parallel()

	t.Run("with no configured source filter, the zip cache is still excluded", func(t *testing.T) {
		t.Parallel()

		spec := testGomodProxySpec()
		st := spec.GomodDeps(testGomodProxySourceOpts(""), llb.Scratch())
		if st == nil {
			t.Fatal("gomod generator succeeded but returned nil state")
		}

		excludes := gomodDepsCopyExcludes(context.Background(), t, *st)
		if !slices.Contains(excludes, "cache/download/**/*.zip") {
			t.Fatalf("expected gomod deps to exclude module zip cache, got %v", excludes)
		}
	})

	t.Run("with a configured source filter, both the zip cache and the configured excludes apply", func(t *testing.T) {
		t.Parallel()

		spec := testGomodProxySpec()
		sOpt := testGomodProxySourceOpts("")
		sOpt.SourceFilter = func() (SourceFilterConfig, error) {
			return SourceFilterConfig{GlobalExcludes: []string{"**/testdata/**"}}, nil
		}

		st := spec.GomodDeps(sOpt, llb.Scratch())
		if st == nil {
			t.Fatal("gomod generator succeeded but returned nil state")
		}

		excludes := gomodDepsCopyExcludes(context.Background(), t, *st)
		if !slices.Contains(excludes, "cache/download/**/*.zip") {
			t.Fatalf("expected gomod deps to still exclude module zip cache, got %v", excludes)
		}
		if !slices.Contains(excludes, "**/testdata/**") {
			t.Fatalf("expected gomod deps to include configured excludes, got %v", excludes)
		}
	})

	t.Run("a failing source filter is surfaced as an error state", func(t *testing.T) {
		t.Parallel()

		spec := testGomodProxySpec()
		sOpt := testGomodProxySourceOpts("")
		sOpt.SourceFilter = func() (SourceFilterConfig, error) {
			return SourceFilterConfig{}, errors.New("boom")
		}

		st := spec.GomodDeps(sOpt, llb.Scratch())
		if st == nil {
			t.Fatal("gomod generator succeeded but returned nil state")
		}

		_, err := st.Marshal(context.Background())
		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("expected marshal to surface source filter error, got %v", err)
		}
	})

	t.Run("a configured source filter cannot re-include the zip cache via negation", func(t *testing.T) {
		t.Parallel()

		// Exclude patterns are matched in order with support for "!"
		// negation (like a .dockerignore file). If a downstream config
		// negated the zip pattern and that negation were applied after the
		// mandatory exclude, it would re-include the zip cache. The
		// mandatory pattern must be the effective, final word.
		spec := testGomodProxySpec()
		sOpt := testGomodProxySourceOpts("")
		sOpt.SourceFilter = func() (SourceFilterConfig, error) {
			return SourceFilterConfig{GlobalExcludes: []string{"!cache/download/**/*.zip"}}, nil
		}

		st := spec.GomodDeps(sOpt, llb.Scratch())
		if st == nil {
			t.Fatal("gomod generator succeeded but returned nil state")
		}

		excludes := gomodDepsCopyExcludes(context.Background(), t, *st)

		const zipPath = "cache/download/github.com/pkg/errors/@v/v0.9.1.zip"
		matched, err := patternmatcher.Matches(zipPath, excludes)
		if err != nil {
			t.Fatalf("failed to evaluate exclude patterns: %v", err)
		}
		if !matched {
			t.Fatalf("expected zip cache path %q to still be excluded despite a negated configured pattern, got excludes %v", zipPath, excludes)
		}
	})
}

func TestGomodDepsUsesGomodProxy(t *testing.T) {
	t.Parallel()

	const proxy = "http://proxy.example:5000,direct"
	spec := testGomodProxySpec()
	st := spec.GomodDeps(testGomodProxySourceOpts(proxy), llb.Scratch())
	if st == nil {
		t.Fatal("gomod generator succeeded but returned nil state")
	}

	env := gomodDownloadExecEnv(context.Background(), t, *st)
	if !slices.Contains(env, "GOPROXY="+proxy) {
		t.Fatalf("expected gomod exec env to include GOPROXY=%q, got %v", proxy, env)
	}
}

func TestGomodDepsSkipsEmptyGomodProxy(t *testing.T) {
	t.Parallel()

	spec := testGomodProxySpec()
	st := spec.GomodDeps(testGomodProxySourceOpts(""), llb.Scratch())
	if st == nil {
		t.Fatal("gomod generator succeeded but returned nil state")
	}

	env := gomodDownloadExecEnv(context.Background(), t, *st)
	for _, item := range env {
		if strings.HasPrefix(item, "GOPROXY=") {
			t.Fatalf("expected empty gomod proxy to omit GOPROXY, got %v", env)
		}
	}
}

func TestGomodProxyBuildArgIsKnown(t *testing.T) {
	t.Parallel()

	spec := &Spec{}
	err := spec.SubstituteArgs(map[string]string{
		BuildArgDalecGomodProxy: "http://proxy.example:5000",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGomodPatchUsesGomodProxy(t *testing.T) {
	t.Parallel()

	const proxy = "http://proxy.example:5000,direct"
	gen := &SourceGenerator{
		Gomod: &GeneratorGomod{
			Edits: &GomodEdits{
				Replace: []GomodReplace{
					{Original: "example.com/old", Update: "example.com/new v1.2.3"},
				},
			},
		},
	}

	st, err := (&Spec{}).generateGomodPatchStateForSource(gomodGeneratorOpts{
		sourceName:  "src",
		gen:         gen,
		sourceState: llb.Scratch(),
		worker:      llb.Scratch(),
		extraEnvs: map[string]string{
			"GOPROXY": proxy,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("gomod patch generation succeeded but returned nil state")
	}

	env := gomodPatchExecEnv(context.Background(), t, *st)
	if !slices.Contains(env, "GOPROXY="+proxy) {
		t.Fatalf("expected gomod patch exec env to include GOPROXY=%q, got %v", proxy, env)
	}
}

func testGomodProxySpec() *Spec {
	return &Spec{
		Sources: map[string]Source{
			"src": {
				Git: &SourceGit{
					URL:    "https://example.com/repo.git",
					Commit: "0123456789abcdef",
				},
				Generate: []*SourceGenerator{
					{Gomod: &GeneratorGomod{}},
				},
			},
		},
	}
}

func testGomodProxySourceOpts(proxy string) SourceOpts {
	sOpt := SourceOpts{
		GetContext: func(name string, opts ...llb.LocalOption) (*llb.State, error) {
			st := llb.Local(name, opts...)
			return &st, nil
		},
		GitCredHelperOpt: func() (llb.RunOption, error) {
			st := llb.Scratch().File(llb.Mkfile("/frontend", 0o755, []byte("#!/usr/bin/env bash\nexit 0\n")))
			return RunOptFunc(func(ei *llb.ExecInfo) {
				llb.AddMount("/usr/local/bin/frontend", st, llb.SourcePath("/frontend")).SetRunOption(ei)
			}), nil
		},
	}
	if proxy != "" {
		sOpt.ExtraEnvs = map[string]string{
			"GOPROXY": proxy,
		}
	}
	return sOpt
}

func gomodDownloadExecEnv(ctx context.Context, t *testing.T, st llb.State) []string {
	t.Helper()

	for _, op := range sourceOpsFromState(ctx, t, st) {
		exec := op.GetExec()
		if exec == nil {
			continue
		}

		env := exec.Meta.Env
		if slices.Contains(env, "GOPATH=/go") && slices.Contains(env, "TMP_GOMODCACHE=/tmp/dalec/gomod-proxy-cache") {
			return env
		}
	}

	t.Fatal("expected gomod download exec")
	return nil
}

func gomodPatchExecEnv(ctx context.Context, t *testing.T, st llb.State) []string {
	t.Helper()

	for _, op := range sourceOpsFromState(ctx, t, st) {
		exec := op.GetExec()
		if exec == nil {
			continue
		}

		if slices.Contains(exec.Meta.Args, "/gomod-patch.sh") {
			return exec.Meta.Env
		}
	}

	t.Fatal("expected gomod patch exec")
	return nil
}

// gomodDepsCopyExcludes returns the ExcludePatterns of the final source-filter
// copy applied by [Spec.GomodDeps]. That copy is identified as the one whose
// destination is the state root ("/") and whose CopyDirContentsOnly is set,
// matching what [SourceFilter] produces.
func gomodDepsCopyExcludes(ctx context.Context, t *testing.T, st llb.State) []string {
	t.Helper()

	for _, op := range sourceOpsFromState(ctx, t, st) {
		fileOp := op.GetFile()
		if fileOp == nil {
			continue
		}
		for _, action := range fileOp.Actions {
			cp := action.GetCopy()
			if cp == nil {
				continue
			}
			if cp.Dest == "/" && cp.DirCopyContents {
				return cp.ExcludePatterns
			}
		}
	}

	t.Fatal("expected gomod deps source-filter copy")
	return nil
}
