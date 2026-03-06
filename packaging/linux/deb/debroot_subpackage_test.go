package deb

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestDebrootSubPackageInstallFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"libs": {
						Description: "Library files",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"mylib-cli": {},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// Subpackage should produce an .install file with the resolved name
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-libs.install"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-libs.install to be generated")

	// The install file should reference the subpackage's destination path
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "debian/example-libs"))
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "mylib-cli"))
}

func TestDebrootSubPackageCustomName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"libs": {
						Name:        "custom-pkg-name",
						Description: "Library files",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"mybin": {},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// Should use the custom name, not "example-libs"
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "custom-pkg-name.install"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected custom-pkg-name.install to be generated")
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "debian/custom-pkg-name"))

	// Should NOT produce a file with the default name
	mkfile2, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-libs.install"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile2 == nil, "should not generate example-libs.install when custom name is set")
}

func TestDebrootSubPackagePostinst(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"svc": {
						Description: "Service package",
						Artifacts: &dalec.Artifacts{
							Users: []dalec.AddUserConfig{
								{Name: "svcuser"},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// Subpackage should produce a postinst file with the resolved name
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-svc.postinst"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-svc.postinst to be generated")
	assert.Equal(t, int32(0o700), mkfile.Mode)
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("#DEBHELPER#")))
	assert.Assert(t, bytes.Contains(mkfile.Data, []byte("useradd svcuser")))
}

func TestDebrootSubPackageNoPostinstWhenEmpty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"libs": {
						Description: "Library files",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"mylib": {},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// Should NOT produce a postinst for a subpackage that has no users/groups/ownership
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-libs.postinst"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile == nil, "should not generate postinst for subpackage with no post-install actions")
}

func TestDebrootSubPackageDirsFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"data": {
						Description: "Data package",
						Artifacts: &dalec.Artifacts{
							Directories: &dalec.CreateArtifactDirectories{
								Config: map[string]dalec.ArtifactDirConfig{
									"myapp": {},
								},
								State: map[string]dalec.ArtifactDirConfig{
									"myapp": {},
								},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-data.dirs"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-data.dirs to be generated")
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "/etc/myapp"))
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "/var/lib/myapp"))
}

func TestDebrootSubPackageLinksFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"compat": {
						Description: "Compat symlinks",
						Artifacts: &dalec.Artifacts{
							Links: []dalec.ArtifactSymlinkConfig{
								{Source: "/usr/bin/mybin", Dest: "/usr/bin/mybin-compat"},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-compat.links"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-compat.links to be generated")
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "usr/bin/mybin"))
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "usr/bin/mybin-compat"))
}

func TestDebrootSubPackageFixPerms(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Artifacts: dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{
				"primarybin": {Permissions: 0o755},
			},
		},
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"tools": {
						Description: "Extra tools",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"subtool": {Permissions: 0o750},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "dalec/fix_perms.sh"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected fix_perms.sh to be generated")

	content := string(mkfile.Data)
	// Primary package permissions
	assert.Assert(t, cmp.Contains(content, "debian/example"))
	assert.Assert(t, cmp.Contains(content, "primarybin"))
	// Subpackage permissions
	assert.Assert(t, cmp.Contains(content, "debian/example-tools"))
	assert.Assert(t, cmp.Contains(content, "subtool"))
}

func TestDebrootSubPackageDocsFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"doc": {
						Description: "Documentation package",
						Artifacts: &dalec.Artifacts{
							Docs: map[string]dalec.ArtifactConfig{
								"README.md": {},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-doc.docs"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-doc.docs to be generated")
	assert.Assert(t, cmp.Contains(string(mkfile.Data), "README.md"))
}

func TestDebrootMultipleSubPackages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"libs": {
						Description: "Libraries",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"lib-thing": {},
							},
						},
					},
					"tools": {
						Description: "Tools",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"extra-tool": {},
							},
						},
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// Both subpackages should produce .install files
	libsInstall, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-libs.install"))
	assert.NilError(t, err)
	assert.Assert(t, libsInstall != nil, "expected example-libs.install")
	assert.Assert(t, cmp.Contains(string(libsInstall.Data), "lib-thing"))

	toolsInstall, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-tools.install"))
	assert.NilError(t, err)
	assert.Assert(t, toolsInstall != nil, "expected example-tools.install")
	assert.Assert(t, cmp.Contains(string(toolsInstall.Data), "extra-tool"))
}

func TestDebrootSubPackageControlFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"libs": {
						Description: "Library files",
						Dependencies: &dalec.SubPackageDependencies{
							Runtime: dalec.PackageDependencyList{
								"libfoo": dalec.PackageConstraints{},
							},
						},
					},
					"tools": {
						Description: "Extra tools",
					},
				},
			},
		},
	}

	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "control"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected control file to be generated")

	content := string(mkfile.Data)

	// Primary package stanza should be present
	assert.Assert(t, cmp.Contains(content, "Package:example"))

	// Subpackage stanzas should be present
	assert.Assert(t, cmp.Contains(content, "Package: example-libs"))
	assert.Assert(t, cmp.Contains(content, "Package: example-tools"))

	// Subpackage descriptions
	assert.Assert(t, cmp.Contains(content, "Description: Library files"))
	assert.Assert(t, cmp.Contains(content, "Description: Extra tools"))

	// Runtime dep from subpackage
	assert.Assert(t, cmp.Contains(content, "libfoo"))
}

func TestDebrootSubPackageRulesOverridePerms(t *testing.T) {
	t.Parallel()

	t.Run("subpackage perms triggers override", func(t *testing.T) {
		ctx := context.Background()
		// Primary has no custom perms, but subpackage does
		spec := &dalec.Spec{
			Name:        "example",
			Description: "Example package",
			Version:     "1.0.0",
			Revision:    "1",
			License:     "Apache-2.0",
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"tools": {
							Description: "Extra tools",
							Artifacts: &dalec.Artifacts{
								Binaries: map[string]dalec.ArtifactConfig{
									"subtool": {Permissions: 0o750},
								},
							},
						},
					},
				},
			},
		}

		st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
		def, err := st.Marshal(ctx)
		assert.NilError(t, err)

		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "rules"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil)

		content := string(mkfile.Data)
		assert.Assert(t, cmp.Contains(content, "override_dh_fixperms"))
		assert.Assert(t, cmp.Contains(content, "fix_perms.sh"))
	})

	t.Run("no perms no override", func(t *testing.T) {
		ctx := context.Background()
		spec := &dalec.Spec{
			Name:        "example",
			Description: "Example package",
			Version:     "1.0.0",
			Revision:    "1",
			License:     "Apache-2.0",
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"tools": {
							Description: "Extra tools",
							Artifacts: &dalec.Artifacts{
								Binaries: map[string]dalec.ArtifactConfig{
									"subtool": {},
								},
							},
						},
					},
				},
			},
		}

		st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
		def, err := st.Marshal(ctx)
		assert.NilError(t, err)

		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "rules"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil)

		content := string(mkfile.Data)
		assert.Assert(t, !bytes.Contains(mkfile.Data, []byte("override_dh_fixperms")), "should not contain fixperms override when no custom perms: %s", content)
	})
}

func TestDebrootSubPackageRulesOverrideSystemd(t *testing.T) {
	t.Parallel()

	t.Run("subpackage units emits dh_installsystemd with package flag", func(t *testing.T) {
		ctx := context.Background()
		spec := &dalec.Spec{
			Name:        "example",
			Description: "Example package",
			Version:     "1.0.0",
			Revision:    "1",
			License:     "Apache-2.0",
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"svc": {
							Description: "Service package",
							Artifacts: &dalec.Artifacts{
								Systemd: &dalec.SystemdConfiguration{
									Units: map[string]dalec.SystemdUnitConfig{
										"mysvc.service": {Enable: true},
									},
								},
							},
						},
					},
				},
			},
		}

		st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
		def, err := st.Marshal(ctx)
		assert.NilError(t, err)

		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "rules"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil)

		content := string(mkfile.Data)
		assert.Assert(t, cmp.Contains(content, "override_dh_installsystemd"))
		// Should use -p flag for subpackage
		assert.Assert(t, cmp.Contains(content, "-pexample-svc"))
		assert.Assert(t, cmp.Contains(content, "--name=mysvc"))
	})

	t.Run("primary and subpackage units both present", func(t *testing.T) {
		ctx := context.Background()
		spec := &dalec.Spec{
			Name:        "example",
			Description: "Example package",
			Version:     "1.0.0",
			Revision:    "1",
			License:     "Apache-2.0",
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"primary.service": {Enable: true},
					},
				},
			},
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"svc": {
							Description: "Service package",
							Artifacts: &dalec.Artifacts{
								Systemd: &dalec.SystemdConfiguration{
									Units: map[string]dalec.SystemdUnitConfig{
										"subsvc.service": {Enable: false},
									},
								},
							},
						},
					},
				},
			},
		}

		st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
		def, err := st.Marshal(ctx)
		assert.NilError(t, err)

		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "rules"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil)

		content := string(mkfile.Data)
		// Primary unit: no -p flag
		assert.Assert(t, cmp.Contains(content, "dh_installsystemd --name=primary\n"))
		// Subpackage unit: -p flag, --no-enable
		assert.Assert(t, cmp.Contains(content, "-pexample-svc --name=subsvc --no-enable"))
	})
}

func TestDebrootSubPackageCustomSystemdPostinst(t *testing.T) {
	t.Parallel()

	t.Run("subpackage mixed enable generates custom postinst", func(t *testing.T) {
		ctx := context.Background()
		spec := &dalec.Spec{
			Name:        "example",
			Description: "Example package",
			Version:     "1.0.0",
			Revision:    "1",
			License:     "Apache-2.0",
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"svc": {
							Description: "Service package",
							Artifacts: &dalec.Artifacts{
								Systemd: &dalec.SystemdConfiguration{
									Units: map[string]dalec.SystemdUnitConfig{
										// Same basename "foo" with mixed enable — triggers custom enable
										"foo.service": {Enable: true},
										"foo.socket":  {Enable: false},
									},
								},
							},
						},
					},
				},
			},
		}

		st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
		def, err := st.Marshal(ctx)
		assert.NilError(t, err)

		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "dalec/"+customSystemdPostinstFile))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected custom systemd postinst partial to be generated for subpackage with mixed enable")

		content := string(mkfile.Data)
		assert.Assert(t, cmp.Contains(content, "foo.service"))
		assert.Assert(t, cmp.Contains(content, "foo.socket"))
	})

	t.Run("no mixed enable no custom postinst file", func(t *testing.T) {
		ctx := context.Background()
		spec := &dalec.Spec{
			Name:        "example",
			Description: "Example package",
			Version:     "1.0.0",
			Revision:    "1",
			License:     "Apache-2.0",
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"svc": {
							Description: "Service package",
							Artifacts: &dalec.Artifacts{
								Systemd: &dalec.SystemdConfiguration{
									Units: map[string]dalec.SystemdUnitConfig{
										"bar.service": {Enable: true},
									},
								},
							},
						},
					},
				},
			},
		}

		st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
		def, err := st.Marshal(ctx)
		assert.NilError(t, err)

		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "dalec/"+customSystemdPostinstFile))
		assert.NilError(t, err)
		assert.Assert(t, mkfile == nil, "should not generate custom systemd postinst when no mixed enable")
	})
}

func TestDebrootSubPackageWrongTarget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Example package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Targets: map[string]dalec.Target{
			"otherdistro": {
				Packages: map[string]dalec.SubPackage{
					"libs": {
						Description: "Library files",
						Artifacts: &dalec.Artifacts{
							Binaries: map[string]dalec.ArtifactConfig{
								"mylib": {},
							},
						},
					},
				},
			},
		},
	}

	// Build for "testdistro" but packages are defined under "otherdistro"
	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	// Should NOT produce any subpackage files for a different target
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-libs.install"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile == nil, "should not generate subpackage files for wrong target")
}
