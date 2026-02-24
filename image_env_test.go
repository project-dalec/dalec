package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestMergeImageConfigEnvReplacement(t *testing.T) {
	t.Run("env vars from image config should replace base image env vars", func(t *testing.T) {
		// Setup: base image with PATH environment variable
		dst := &DockerImageConfig{}
		dst.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root"}

		// Image config wants to override PATH
		src := &ImageConfig{
			Env: []string{"PATH=/custom/bin:/custom/usr/bin"},
		}

		err := MergeImageConfig(dst, src)
		assert.NilError(t, err)

		// PATH should be replaced, not duplicated
		assert.Check(t, cmp.Len(dst.Env, 2))
		assert.Check(t, cmp.Contains(dst.Env, "PATH=/custom/bin:/custom/usr/bin"))
		assert.Check(t, cmp.Contains(dst.Env, "HOME=/root"))

		// Should NOT have duplicate PATH entries
		pathCount := 0
		for _, env := range dst.Env {
			if len(env) >= 5 && env[:5] == "PATH=" {
				pathCount++
			}
		}
		assert.Check(t, cmp.Equal(pathCount, 1), "Expected exactly 1 PATH env var, got %d", pathCount)
	})

	t.Run("env vars with different names should be appended", func(t *testing.T) {
		dst := &DockerImageConfig{}
		dst.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root"}

		src := &ImageConfig{
			Env: []string{"USER=myuser", "LANG=en_US.UTF-8"},
		}

		err := MergeImageConfig(dst, src)
		assert.NilError(t, err)

		assert.Check(t, cmp.Len(dst.Env, 4))
		assert.Check(t, cmp.Contains(dst.Env, "PATH=/usr/bin:/bin"))
		assert.Check(t, cmp.Contains(dst.Env, "HOME=/root"))
		assert.Check(t, cmp.Contains(dst.Env, "USER=myuser"))
		assert.Check(t, cmp.Contains(dst.Env, "LANG=en_US.UTF-8"))
	})

	t.Run("multiple env var replacements", func(t *testing.T) {
		dst := &DockerImageConfig{}
		dst.Env = []string{"PATH=/usr/bin:/bin", "HOME=/root", "USER=root"}

		src := &ImageConfig{
			Env: []string{"PATH=/custom/bin", "USER=customuser"},
		}

		err := MergeImageConfig(dst, src)
		assert.NilError(t, err)

		assert.Check(t, cmp.Len(dst.Env, 3))
		assert.Check(t, cmp.Contains(dst.Env, "PATH=/custom/bin"))
		assert.Check(t, cmp.Contains(dst.Env, "HOME=/root"))
		assert.Check(t, cmp.Contains(dst.Env, "USER=customuser"))
	})

	t.Run("env var without equals sign should not cause issues", func(t *testing.T) {
		dst := &DockerImageConfig{}
		dst.Env = []string{"PATH=/usr/bin:/bin", "VALID_VAR"}

		src := &ImageConfig{
			Env: []string{"PATH=/custom/bin", "ANOTHER_VALID_VAR"},
		}

		err := MergeImageConfig(dst, src)
		assert.NilError(t, err)

		assert.Check(t, cmp.Len(dst.Env, 3))
		assert.Check(t, cmp.Contains(dst.Env, "PATH=/custom/bin"))
		assert.Check(t, cmp.Contains(dst.Env, "VALID_VAR"))
		assert.Check(t, cmp.Contains(dst.Env, "ANOTHER_VALID_VAR"))
	})
}
