package test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
)

func TestNamedImages(t *testing.T) {
	for _, distro := range []string{"azlinux3", "jammy"} {
		t.Run(distro, func(t *testing.T) {
			for _, name := range []string{"primary", "tools", "dependent", "custom-base", "all", ""} {
				t.Run("image="+name, func(t *testing.T) {
					ctx := startTestSpan(baseCtx, t)
					spec := namedImagesSpec(t, distro)
					target := distro + "/container"
					if name != "" {
						target += "/" + name
					}
					testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
						res := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(target)))
						var img dalec.DockerImageSpec
						assert.NilError(t, json.Unmarshal(res.Metadata[exptypes.ExporterImageConfigKey], &img))
						if name == "" {
							assert.DeepEqual(t, img.Config.Entrypoint, []string{"/default"})
						} else {
							assert.DeepEqual(t, img.Config.Entrypoint, []string{"/" + name})
							_, inherited := img.Config.Labels["default-only"]
							assert.Assert(t, !inherited)
						}
					})
				})
			}
		})
	}
}

func namedImagesSpec(t *testing.T, distro string) dalec.Spec {
	base := "docker.io/library/ubuntu:jammy"
	if distro == "azlinux3" {
		base = "mcr.microsoft.com/azurelinux/base/core:3.0"
	}
	spec := testLinuxSpec(t, dalec.Spec{
		Name: "named-image-test",
		Sources: map[string]dalec.Source{
			"primary": {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{Contents: "primary"}}},
			"tools":   {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{Contents: "tools"}}},
			"extra":   {Inline: &dalec.SourceInline{File: &dalec.SourceInlineFile{Contents: "extra"}}},
		},
		Artifacts: dalec.Artifacts{Binaries: map[string]dalec.ArtifactConfig{"primary": {}}},
		Image: &dalec.ImageConfig{
			Entrypoint: "/default",
			Labels:     map[string]string{"default-only": "yes"},
		},
		Tests: []*dalec.TestSpec{{
			Name:  "root baseline",
			Files: map[string]dalec.FileCheckOutput{"/etc/os-release": {}},
		}},
	})
	files := func(primary, tools, extra bool) []*dalec.TestSpec {
		return []*dalec.TestSpec{{
			Name: "image package selection",
			Files: map[string]dalec.FileCheckOutput{
				"/usr/bin/primary": {NotExist: !primary},
				"/usr/bin/tools":   {NotExist: !tools},
				"/usr/bin/extra":   {NotExist: !extra},
			},
		}}
	}
	spec.Targets = map[string]dalec.Target{distro: {
		Packages: map[string]dalec.SubPackage{
			"tools": {
				Name:        "custom-tools",
				Description: "Standalone tools",
				Artifacts:   &dalec.Artifacts{Binaries: map[string]dalec.ArtifactConfig{"tools": {}}},
			},
			"extra": {
				Description: "Extra with primary dependency",
				Artifacts:   &dalec.Artifacts{Binaries: map[string]dalec.ArtifactConfig{"extra": {}}},
				Dependencies: &dalec.SubPackageDependencies{Runtime: dalec.PackageDependencyList{
					spec.Name: {},
				}},
			},
		},
		Tests: []*dalec.TestSpec{{
			Name:  "target baseline",
			Files: map[string]dalec.FileCheckOutput{"/etc/os-release": {}},
		}},
		Images: map[string]dalec.NamedImage{
			"primary": {
				ImageConfig: dalec.ImageConfig{Entrypoint: "/primary"},
				Packages:    []string{spec.Name},
				Tests:       files(true, false, false),
			},
			"tools": {
				ImageConfig: dalec.ImageConfig{Entrypoint: "/tools"},
				Packages:    []string{"custom-tools"},
				Tests:       files(false, true, false),
			},
			"dependent": {
				ImageConfig: dalec.ImageConfig{Entrypoint: "/dependent"},
				Packages:    []string{spec.Name + "-extra"},
				Tests:       files(true, false, true),
			},
			"custom-base": {
				ImageConfig: dalec.ImageConfig{
					Entrypoint: "/custom-base",
					Base:       base,
					Post: &dalec.PostInstall{Symlinks: map[string]dalec.SymlinkTarget{
						"/usr/bin/tools": {Paths: []string{"/tools-link"}},
					}},
				},
				Packages: []string{"custom-tools"},
				Tests: append(files(false, true, false), &dalec.TestSpec{
					Name: "image post-install configuration",
					Files: map[string]dalec.FileCheckOutput{
						"/tools-link": {CheckOutput: dalec.CheckOutput{Equals: "tools"}},
					},
				}),
			},
			"all": {
				ImageConfig: dalec.ImageConfig{Entrypoint: "/all"},
				Tests:       files(true, true, true),
			},
		},
	}}
	return spec
}

func TestNamedWindowsImages(t *testing.T) {
	for _, name := range []string{"tools", "all", ""} {
		t.Run("image="+name, func(t *testing.T) {
			ctx := startTestSpan(baseCtx, t)
			spec := namedImagesSpec(t, "windowscross")
			spec.Dependencies = nil
			spec.Tests = nil
			target := spec.Targets["windowscross"]
			target.Tests = nil
			delete(target.Packages, "extra")
			target.Images = map[string]dalec.NamedImage{
				"tools": {
					ImageConfig: dalec.ImageConfig{Entrypoint: "/tools"},
					Packages:    []string{"custom-tools"},
				},
				"all": {ImageConfig: dalec.ImageConfig{Entrypoint: "/all"}},
			}
			spec.Targets["windowscross"] = target
			buildTarget := "windowscross/container"
			if name != "" {
				buildTarget += "/" + name
			}
			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				res := solveT(ctx, t, gwc, newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(buildTarget), withWindowsAmd64))
				ref, err := res.SingleRef()
				assert.NilError(t, err)
				data, err := ref.ReadFile(ctx, gwclient.ReadRequest{Filename: "/Windows/System32/tools"})
				assert.NilError(t, err)
				assert.Equal(t, string(data), "tools")
				var img dalec.DockerImageSpec
				assert.NilError(t, json.Unmarshal(res.Metadata[exptypes.ExporterImageConfigKey], &img))
				if name != "" {
					assert.DeepEqual(t, img.Config.Entrypoint, []string{"/" + name})
					_, inherited := img.Config.Labels["default-only"]
					assert.Assert(t, !inherited)
				}
				_, err = ref.StatFile(ctx, gwclient.StatRequest{Path: "/Windows/System32/primary"})
				if name == "tools" {
					assert.Assert(t, err != nil)
				} else {
					assert.NilError(t, err)
				}
			})
		})
	}
}
