package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestMergeSpecImage(t *testing.T) {
	t.Run("nil spec image returns empty config", func(t *testing.T) {
		spec := &Spec{}
		cfg := MergeSpecImage(spec, "foo")
		assert.Check(t, cmp.DeepEqual(cfg, &ImageConfig{}))
	})

	t.Run("spec image with no target returns spec image", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/sh",
				Cmd:        "-c",
				User:       "1000",
				WorkingDir: "/app",
				StopSignal: "SIGTERM",
			},
		}
		cfg := MergeSpecImage(spec, "foo")
		assert.Check(t, cmp.Equal(cfg.Entrypoint, "/bin/sh"))
		assert.Check(t, cmp.Equal(cfg.Cmd, "-c"))
		assert.Check(t, cmp.Equal(cfg.User, "1000"))
		assert.Check(t, cmp.Equal(cfg.WorkingDir, "/app"))
		assert.Check(t, cmp.Equal(cfg.StopSignal, "SIGTERM"))
	})

	t.Run("target overrides user", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				User: "1000",
			},
			Targets: map[string]Target{
				"azlinux3": {
					Image: &ImageConfig{
						User: "999",
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "azlinux3")
		assert.Check(t, cmp.Equal(cfg.User, "999"))
	})

	t.Run("target sets user when spec has none", func(t *testing.T) {
		spec := &Spec{
			Targets: map[string]Target{
				"azlinux3": {
					Image: &ImageConfig{
						User: "999",
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "azlinux3")
		assert.Check(t, cmp.Equal(cfg.User, "999"))
	})

	t.Run("spec user preserved when target does not set it", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				User: "1000",
			},
			Targets: map[string]Target{
				"azlinux3": {
					Image: &ImageConfig{
						Cmd: "echo hello",
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "azlinux3")
		assert.Check(t, cmp.Equal(cfg.User, "1000"))
	})

	t.Run("target overrides all string fields", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Entrypoint: "/bin/old",
				Cmd:        "old",
				WorkingDir: "/old",
				StopSignal: "SIGINT",
				Base:       "old:latest",
				User:       "root",
			},
			Targets: map[string]Target{
				"t1": {
					Image: &ImageConfig{
						Entrypoint: "/bin/new",
						Cmd:        "new",
						WorkingDir: "/new",
						StopSignal: "SIGTERM",
						Base:       "new:latest",
						User:       "nobody",
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "t1")
		assert.Check(t, cmp.Equal(cfg.Entrypoint, "/bin/new"))
		assert.Check(t, cmp.Equal(cfg.Cmd, "new"))
		assert.Check(t, cmp.Equal(cfg.WorkingDir, "/new"))
		assert.Check(t, cmp.Equal(cfg.StopSignal, "SIGTERM"))
		assert.Check(t, cmp.Equal(cfg.Base, "new:latest"))
		assert.Check(t, cmp.Equal(cfg.User, "nobody"))
	})

	t.Run("target env appends to spec env", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Env: []string{"A=1"},
			},
			Targets: map[string]Target{
				"t1": {
					Image: &ImageConfig{
						Env: []string{"B=2"},
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "t1")
		assert.Check(t, cmp.DeepEqual(cfg.Env, []string{"A=1", "B=2"}))
	})

	t.Run("target volumes merge with spec volumes", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Volumes: map[string]struct{}{"/data": {}},
			},
			Targets: map[string]Target{
				"t1": {
					Image: &ImageConfig{
						Volumes: map[string]struct{}{"/logs": {}},
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "t1")
		assert.Check(t, cmp.Len(cfg.Volumes, 2))
		_, hasData := cfg.Volumes["/data"]
		_, hasLogs := cfg.Volumes["/logs"]
		assert.Check(t, hasData)
		assert.Check(t, hasLogs)
	})

	t.Run("target labels merge with spec labels", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				Labels: map[string]string{"a": "1"},
			},
			Targets: map[string]Target{
				"t1": {
					Image: &ImageConfig{
						Labels: map[string]string{"b": "2"},
					},
				},
			},
		}
		cfg := MergeSpecImage(spec, "t1")
		assert.Check(t, cmp.Len(cfg.Labels, 2))
		assert.Check(t, cmp.Equal(cfg.Labels["a"], "1"))
		assert.Check(t, cmp.Equal(cfg.Labels["b"], "2"))
	})

	t.Run("nonexistent target key returns spec image unchanged", func(t *testing.T) {
		spec := &Spec{
			Image: &ImageConfig{
				User:       "1000",
				Entrypoint: "/bin/sh",
			},
		}
		cfg := MergeSpecImage(spec, "nonexistent")
		assert.Check(t, cmp.Equal(cfg.User, "1000"))
		assert.Check(t, cmp.Equal(cfg.Entrypoint, "/bin/sh"))
	})
}
