package dalec

import (
	"context"
	"fmt"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"gotest.tools/v3/assert"
)

// determinismRuns is how many times a generator is re-run when checking that
// its output does not depend on Go's randomized map iteration order. A single
// extra run (i.e. 2 total) only catches a regression ~50% of the time for a
// two-key map, so we marshal many times: marshaling is cheap and each
// additional run drives the false-pass probability toward zero.
const determinismRuns = 25

// requireDeterministicLLB rebuilds the state from scratch on every run (so the
// previously-unsorted map iteration executes again) and asserts the marshaled
// op definitions are byte-identical. Only Def is compared: it holds the op
// protos whose digests drive BuildKit's cache. Metadata is intentionally
// ignored because it carries per-build noise such as progress-group IDs.
func requireDeterministicLLB(ctx context.Context, t *testing.T, build func() llb.State) {
	t.Helper()

	want, err := build().Marshal(ctx)
	assert.NilError(t, err)

	for i := 1; i < determinismRuns; i++ {
		got, err := build().Marshal(ctx)
		assert.NilError(t, err)
		assert.DeepEqual(t, got.Def, want.Def)
	}
}

// manyEnv returns an env map with enough entries that a single unsorted
// iteration is overwhelmingly likely to differ between runs. The prefix keeps
// the keys of two maps from overlapping when both are used in one state.
func manyEnv(prefix string) map[string]string {
	env := make(map[string]string, 12)
	for i := 0; i < 12; i++ {
		env[fmt.Sprintf("%s_VAR_%02d", prefix, i)] = fmt.Sprintf("value-%d", i)
	}
	return env
}

func TestImageCommandEnvProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	sOpt := SourceOpts{
		GetContext: func(name string, opts ...llb.LocalOption) (*llb.State, error) {
			st := llb.Local(name, opts...)
			return &st, nil
		},
	}

	buildFrom := func(cmd *Command) func() llb.State {
		return func() llb.State {
			src := Source{DockerImage: &SourceDockerImage{Ref: "localhost:0/does/not/exist:latest", Cmd: cmd}}
			src.fillDefaults()
			name := ""
			if !src.IsDir() {
				name = "test"
			}
			return src.ToState(name, sOpt)
		}
	}

	t.Run("command env ordering is stable", func(t *testing.T) {
		cmd := &Command{
			Dir:   "/work",
			Env:   manyEnv("CMD"),
			Steps: []*BuildStep{{Command: "true"}},
		}
		requireDeterministicLLB(ctx, t, buildFrom(cmd))
	})

	t.Run("step env ordering is stable", func(t *testing.T) {
		cmd := &Command{
			Steps: []*BuildStep{{Command: "true", Env: manyEnv("STEP")}},
		}
		requireDeterministicLLB(ctx, t, buildFrom(cmd))
	})

	t.Run("command and step env with different keys stay stable", func(t *testing.T) {
		cmd := &Command{
			Dir:   "/work",
			Env:   manyEnv("CMD"),
			Steps: []*BuildStep{{Command: "true", Env: manyEnv("STEP")}},
		}
		requireDeterministicLLB(ctx, t, buildFrom(cmd))
	})
}

// TestGetRepoKeysIsDeterministic covers GetRepoKeys, which collects GPG keys
// from an unordered map. The returned names feed downstream import-key command
// arguments and the generated key mounts feed an exec, so GetRepoKeys sorts its
// iteration to keep both stable rather than relying on normalization in lower
// layers.
func TestGetRepoKeysIsDeterministic(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := &RepoPlatformConfig{GPGKeyRoot: "/etc/apt/keyrings"}
	sOpt := SourceOpts{
		GetContext: func(name string, opts ...llb.LocalOption) (*llb.State, error) {
			st := llb.Local(name, opts...)
			return &st, nil
		},
	}

	newConfigs := func() []PackageRepositoryConfig {
		keys := make(map[string]Source, 12)
		for i := 0; i < 12; i++ {
			keys[fmt.Sprintf("key-%02d.gpg", i)] = Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{Contents: fmt.Sprintf("key-material-%d", i)},
				},
			}
		}
		return []PackageRepositoryConfig{{Keys: keys}}
	}

	want := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		want = append(want, fmt.Sprintf("key-%02d.gpg", i))
	}

	t.Run("names come back in sorted order", func(t *testing.T) {
		_, names := GetRepoKeys(newConfigs(), cfg, sOpt)
		assert.DeepEqual(t, names, want)
	})

	t.Run("names are stable across runs", func(t *testing.T) {
		for i := 0; i < determinismRuns; i++ {
			_, names := GetRepoKeys(newConfigs(), cfg, sOpt)
			assert.DeepEqual(t, names, want)
		}
	})

	t.Run("generated key mounts are stable across runs", func(t *testing.T) {
		build := func() llb.State {
			runOpt, _ := GetRepoKeys(newConfigs(), cfg, sOpt)
			return llb.Scratch().Run(llb.Args([]string{"true"}), runOpt).Root()
		}
		requireDeterministicLLB(ctx, t, build)
	})
}

func TestGomodAuthProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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

	newSpec := func() *Spec {
		auth := make(map[string]GomodGitAuth, 12)
		for i := 0; i < 12; i++ {
			auth[fmt.Sprintf("host-%02d.example.com", i)] = GomodGitAuth{
				Token: fmt.Sprintf("DALEC_TOKEN_%02d", i),
			}
		}
		return &Spec{
			Sources: map[string]Source{
				"foo": {
					Git: &SourceGit{URL: "https://localhost/test.git", Commit: "deadbeef"},
					Generate: []*SourceGenerator{
						{Gomod: &GeneratorGomod{Auth: auth}},
					},
				},
			},
		}
	}

	build := func() llb.State {
		st := newSpec().GomodDeps(sOpt, llb.Scratch())
		assert.Assert(t, st != nil)
		return *st
	}

	requireDeterministicLLB(ctx, t, build)
}

func TestGomodPatchEnvProducesDeterministicLLB(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	auth := make(map[string]GomodGitAuth, 12)
	for i := 0; i < 12; i++ {
		auth[fmt.Sprintf("host-%02d.example.com", i)] = GomodGitAuth{
			Token: fmt.Sprintf("DALEC_TOKEN_%02d", i),
		}
	}

	gen := &SourceGenerator{
		Gomod: &GeneratorGomod{
			Auth: auth,
			Edits: &GomodEdits{
				Replace: []GomodReplace{
					{Original: "example.com/old", Update: "example.com/new v1.2.3"},
				},
			},
		},
	}

	spec := &Spec{}
	base := llb.Scratch()
	worker := llb.Scratch()

	build := func() llb.State {
		st, err := spec.generateGomodPatchStateForSource("src", gen, base, worker, nil)
		assert.NilError(t, err)
		assert.Assert(t, st != nil)
		return *st
	}

	requireDeterministicLLB(ctx, t, build)
}
