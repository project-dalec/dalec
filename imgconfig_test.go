package dalec

import (
	"encoding/json"
	"maps"
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestGitSourceWebURL(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"https", "https://github.com/coredns/coredns.git", "https://github.com/coredns/coredns"},
		{"trailing slash", "https://github.com/coredns/coredns.git/", "https://github.com/coredns/coredns"},
		{"already normalized", "https://github.com/coredns/coredns", "https://github.com/coredns/coredns"},
		{"credentials and metadata", "https://user:secret@github.com/coredns/coredns.git?token=secret#v1.12.0:src", "https://github.com/coredns/coredns"},
		{"http custom host", "http://git.example.com:8080/team/repo.git", "http://git.example.com:8080/team/repo"},
		{"scp", "git@github.com:coredns/coredns.git", "https://github.com/coredns/coredns"},
		{"ssh", "ssh://git@github.com/coredns/coredns.git", "https://github.com/coredns/coredns"},
		{"ssh standard port", "ssh://git@github.com:22/coredns/coredns.git", "https://github.com/coredns/coredns"},
		{"git protocol", "git://github.com/coredns/coredns.git", "https://github.com/coredns/coredns"},
		{"git standard port", "git://github.com:9418/coredns/coredns.git", "https://github.com/coredns/coredns"},
		{"gitlab subgroup", "git@gitlab.com:group/subgroup/repo.git", "https://gitlab.com/group/subgroup/repo"},
		{"bitbucket", "git@bitbucket.org:team/repo.git", "https://bitbucket.org/team/repo"},
		{"unknown ssh web endpoint", "git@git.example.com:/home/private/repo.git", ""},
		{"unknown git web endpoint", "git://git.example.com/repo.git", ""},
		{"custom ssh port", "ssh://git@github.com:2222/coredns/coredns.git", ""},
		{"file URL", "file:///home/private/repo.git", ""},
		{"absolute path", "/home/private/repo.git", ""},
		{"relative path", "../repo.git", ""},
		{"local host", "https://localhost/repo.git", ""},
		{"fully qualified local host", "https://localhost./repo.git", ""},
		{"local domain", "https://git.local/repo.git", ""},
		{"loopback", "http://127.0.0.1/repo.git", ""},
		{"private IP", "http://10.0.0.1/repo.git", ""},
		{"IPv6 loopback", "http://[::1]/repo.git", ""},
		{"IPv4 mapped loopback", "http://[::ffff:127.0.0.1]/repo.git", ""},
		{"IPv6 zoned link local", "http://[fe80::1%25eth0]/repo.git", ""},
		{"unspecified IP", "http://0.0.0.0/repo.git", ""},
		{"link local", "http://169.254.1.1/repo.git", ""},
		{"empty", "", ""},
		{"no host", "https:///repo.git", ""},
		{"no path", "https://github.com/", ""},
		{"malformed", "https://github.com/repo%zz.git", ""},
		{"unsupported scheme", "ftp://github.com/team/repo.git", ""},
		{"unresolved argument", "https://github.com/${REPO}.git", ""},
		{"home path", "git@github.com:~/private/repo.git", ""},
		{"parent path", "https://github.com/team/../repo.git", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, gitSourceWebURL(tc.input), tc.want)
		})
	}
}

func TestBuildImageConfigSourceLabels(t *testing.T) {
	const upstream = "https://github.com/coredns/coredns"
	gitSource := Source{Git: &SourceGit{URL: upstream + ".git", Commit: "v1.12.0"}}
	for _, tc := range []struct {
		name    string
		sources map[string]Source
		global  map[string]string
		target  map[string]string
		want    map[string]string
	}{
		{
			name: "one git source without image config", sources: map[string]Source{"src": gitSource},
			want: map[string]string{ocispecs.AnnotationSource: upstream},
		},
		{
			name:    "non git sources do not add ambiguity",
			sources: map[string]Source{"src": gitSource, "patch": {HTTP: &SourceHTTP{URL: "https://example.com/fix.patch"}}},
			want:    map[string]string{ocispecs.AnnotationSource: upstream},
		},
		{name: "no sources"},
		{name: "archive", sources: map[string]Source{"src": {HTTP: &SourceHTTP{URL: upstream + "/archive/v1.12.0.tar.gz"}}}},
		{name: "nested build context", sources: map[string]Source{"src": {Build: &SourceBuild{Source: gitSource}}}},
		{name: "multiple git sources", sources: map[string]Source{"src": gitSource, "copy": gitSource}},
		{name: "unsupported source", sources: map[string]Source{"src": {Git: &SourceGit{URL: "/home/private/src"}}}},
		{
			name:    "unsupported second git source is still ambiguous",
			sources: map[string]Source{"src": gitSource, "local": {Git: &SourceGit{URL: "/home/private/src"}}},
		},
		{
			name: "explicit global source", sources: map[string]Source{"src": gitSource},
			global: map[string]string{ocispecs.AnnotationSource: "https://example.com/override"},
			want:   map[string]string{ocispecs.AnnotationSource: "https://example.com/override"},
		},
		{
			name:   "explicit source without git",
			global: map[string]string{ocispecs.AnnotationSource: upstream},
			want:   map[string]string{ocispecs.AnnotationSource: upstream},
		},
		{
			name: "global opt out", sources: map[string]Source{"src": gitSource},
			global: map[string]string{ocispecs.AnnotationSource: ""},
			want:   map[string]string{ocispecs.AnnotationSource: ""},
		},
		{
			name: "target opt out overrides global", sources: map[string]Source{"src": gitSource},
			global: map[string]string{ocispecs.AnnotationSource: upstream},
			target: map[string]string{ocispecs.AnnotationSource: ""},
			want:   map[string]string{ocispecs.AnnotationSource: ""},
		},
		{
			name: "target overrides global opt out", sources: map[string]Source{"src": gitSource},
			global: map[string]string{ocispecs.AnnotationSource: ""},
			target: map[string]string{ocispecs.AnnotationSource: upstream},
			want:   map[string]string{ocispecs.AnnotationSource: upstream},
		},
		{
			name: "legacy explicit source", sources: map[string]Source{"src": gitSource},
			global: map[string]string{legacyImageSourceLabel: "https://example.com/legacy"},
			want:   map[string]string{legacyImageSourceLabel: "https://example.com/legacy"},
		},
		{
			name: "legacy opt out", sources: map[string]Source{"src": gitSource},
			target: map[string]string{legacyImageSourceLabel: ""},
			want:   map[string]string{legacyImageSourceLabel: ""},
		},
		{
			name: "explicit revisions", sources: map[string]Source{"src": gitSource},
			global: map[string]string{ocispecs.AnnotationRevision: "upstream-commit", legacyImageRevisionLabel: "upstream-ref"},
			want: map[string]string{
				ocispecs.AnnotationSource: upstream, ocispecs.AnnotationRevision: "upstream-commit",
				legacyImageRevisionLabel: "upstream-ref",
			},
		},
		{
			name: "explicit labels remain verbatim", sources: map[string]Source{"src": gitSource},
			global: map[string]string{
				ocispecs.AnnotationSource: "", legacyImageSourceLabel: "https://example.com/intentional",
			},
			want: map[string]string{
				ocispecs.AnnotationSource: "", legacyImageSourceLabel: "https://example.com/intentional",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &Spec{Sources: tc.sources, Website: "https://example.com/not-a-repository"}
			if tc.global != nil {
				spec.Image = &ImageConfig{Labels: maps.Clone(tc.global)}
			}
			if tc.target != nil {
				spec.Targets = map[string]Target{"test": {Image: &ImageConfig{Labels: maps.Clone(tc.target)}}}
			}
			for _, inherited := range []bool{false, true} {
				img := &DockerImageSpec{}
				want := maps.Clone(tc.want)
				if want == nil {
					want = make(map[string]string)
				}
				if inherited {
					img.Config.Labels = map[string]string{
						ocispecs.AnnotationSource: "https://example.com/base", ocispecs.AnnotationRevision: "base-commit",
						legacyImageSourceLabel: "https://example.com/legacy-base", legacyImageRevisionLabel: "base-ref",
						"unrelated": "preserved",
					}
					want["unrelated"] = "preserved"
				}
				baseLabels := img.Config.Labels
				originalBase := maps.Clone(baseLabels)
				assert.NilError(t, BuildImageConfig(spec, "test", img))
				assert.Check(t, maps.Equal(img.Config.Labels, want), "inherited=%v: got %v, want %v", inherited, img.Config.Labels, want)
				assert.Check(t, maps.Equal(baseLabels, originalBase), "base config must not be mutated")

				data, err := json.Marshal(img)
				assert.NilError(t, err)
				var exported struct {
					Config struct {
						Labels map[string]string `json:"Labels"`
					} `json:"config"`
				}
				assert.NilError(t, json.Unmarshal(data, &exported))
				assert.Check(t, maps.Equal(exported.Config.Labels, want))
			}
			if spec.Image != nil {
				assert.DeepEqual(t, spec.Image.Labels, tc.global)
			}
			if spec.Targets != nil {
				assert.DeepEqual(t, spec.Targets["test"].Image.Labels, tc.target)
			}
		})
	}
}

func TestBuildImageConfigSourcePlatforms(t *testing.T) {
	spec := &Spec{
		Sources: map[string]Source{"src": {Git: &SourceGit{URL: "https://github.com/coredns/coredns.git"}}},
		Image:   &ImageConfig{Labels: map[string]string{"unrelated": "global"}},
		Targets: map[string]Target{
			"override": {Image: &ImageConfig{Labels: map[string]string{ocispecs.AnnotationSource: "https://example.com/override"}}},
			"opt-out":  {Image: &ImageConfig{Labels: map[string]string{ocispecs.AnnotationSource: ""}}},
		},
	}
	for _, platform := range []ocispecs.Platform{
		{OS: "linux", Architecture: "amd64"},
		{OS: "linux", Architecture: "arm64"},
		{OS: "windows", Architecture: "amd64", OSVersion: "10.0.17763.0"},
		{OS: "windows", Architecture: "amd64", OSVersion: "10.0.20348.0"},
	} {
		for _, target := range []string{"override", "opt-out", "default"} {
			t.Run(platform.OS+"/"+platform.Architecture+"/"+platform.OSVersion+"/"+target, func(t *testing.T) {
				t.Parallel()
				img := &DockerImageSpec{}
				img.Platform = platform
				assert.NilError(t, BuildImageConfig(spec, target, img))
				want := "https://github.com/coredns/coredns"
				if target == "override" {
					want = "https://example.com/override"
				} else if target == "opt-out" {
					want = ""
				}
				assert.Equal(t, img.Config.Labels[ocispecs.AnnotationSource], want)
				assert.Equal(t, img.Config.Labels["unrelated"], "global")
				assert.DeepEqual(t, img.Platform, platform)
			})
		}
	}
}

func TestBuildImageConfigSourceArgs(t *testing.T) {
	spec, err := LoadSpec([]byte(`
name: source-label
description: Source label argument substitution
version: "1"
revision: "1"
license: MIT
args:
  REPOSITORY: example/wrong
sources:
  src:
    git:
      url: https://github.com/${REPOSITORY}.git
      commit: v1.12.0
`))
	assert.NilError(t, err)
	assert.NilError(t, spec.SubstituteArgs(map[string]string{"REPOSITORY": "coredns/coredns"}))
	// Defaults belong to container configuration, not spec loading or package generation.
	assert.Assert(t, spec.Image == nil)
	assert.DeepEqual(t, MergeSpecImage(spec, ""), &ImageConfig{})
	img := &DockerImageSpec{}
	assert.NilError(t, BuildImageConfig(spec, "", img))
	assert.Equal(t, img.Config.Labels[ocispecs.AnnotationSource], "https://github.com/coredns/coredns")
	assert.Assert(t, spec.Image == nil)

	spec.Image = &ImageConfig{Labels: map[string]string{ocispecs.AnnotationSource: "${UPSTREAM}"}}
	spec.Args["UPSTREAM"] = "https://example.com/incorrect"
	assert.NilError(t, spec.SubstituteArgs(map[string]string{"UPSTREAM": "https://example.com/explicit"}))
	assert.NilError(t, BuildImageConfig(spec, "", img))
	assert.Equal(t, img.Config.Labels[ocispecs.AnnotationSource], "https://example.com/explicit")
}

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
				Entrypoint:          "/bin/old",
				Cmd:                 "old",
				WorkingDir:          "/old",
				StopSignal:          "SIGINT",
				Base:                "old:latest",
				User:                "root",
				MinimizationProfile: "default",
			},
			Targets: map[string]Target{
				"t1": {
					Image: &ImageConfig{
						Entrypoint:          "/bin/new",
						Cmd:                 "new",
						WorkingDir:          "/new",
						StopSignal:          "SIGTERM",
						Base:                "new:latest",
						User:                "nobody",
						MinimizationProfile: "default",
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
		assert.Check(t, cmp.Equal(cfg.MinimizationProfile, "default"))
	})

	t.Run("target minimization profile overrides spec profile", func(t *testing.T) {
		spec := &Spec{
			Targets: map[string]Target{
				"t1": {Image: &ImageConfig{MinimizationProfile: "default"}},
			},
		}

		assert.Check(t, cmp.Equal(spec.GetImageMinimizationProfile("t1"), "default"))
		assert.Check(t, cmp.Equal(spec.GetImageMinimizationProfile("other"), ""))
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
