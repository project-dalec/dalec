package deb

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestDebrootSubPackageInstallFile(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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
							ConfigFiles: map[string]dalec.ArtifactConfig{
								"config.yaml": {Name: "renamed.conf", SubPath: "example"},
							},
							Headers: map[string]dalec.ArtifactConfig{
								"include/example.h": {Name: "renamed.h", SubPath: "example"},
							},
							Systemd: &dalec.SystemdConfiguration{
								Dropins: map[string]dalec.SystemdDropinConfig{
									"override.conf": {Name: "renamed-override.conf", Unit: "example.service"},
								},
							},
							DataDirs: map[string]dalec.ArtifactConfig{
								"assets/data": {Name: "renamed-data", SubPath: "example"},
							},
							Libexec: map[string]dalec.ArtifactConfig{
								"helper": {Name: "renamed-helper", SubPath: "example"},
							},
							Libs: map[string]dalec.ArtifactConfig{
								"libexample.so": {Name: "librenamed.so", SubPath: "example"},
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

	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-libs.install"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-libs.install to be generated")

	content := string(mkfile.Data)
	tests := []struct {
		name     string
		expected string
	}{
		{
			name:     "binaries install under usr bin",
			expected: "do_install debian/example-libs/usr/bin debian/example-libs/usr/bin/mylib-cli mylib-cli\n",
		},
		{
			name:     "config files install under etc",
			expected: "do_install debian/example-libs/etc/example debian/example-libs/etc/example/renamed.conf config.yaml\n",
		},
		{
			name:     "headers install under usr include",
			expected: "do_install debian/example-libs/usr/include/example debian/example-libs/usr/include/example/renamed.h include/example.h\n",
		},
		{
			name:     "systemd dropins install under their unit",
			expected: "do_install debian/example-libs/lib/systemd/system/example.service.d debian/example-libs/lib/systemd/system/example.service.d/renamed-override.conf override.conf\n",
		},
		{
			name:     "data files install under usr share",
			expected: "do_install debian/example-libs/usr/share/example debian/example-libs/usr/share/example/renamed-data assets/data\n",
		},
		{
			name:     "libexec files install under usr libexec",
			expected: "do_install debian/example-libs/usr/libexec/example debian/example-libs/usr/libexec/example/renamed-helper helper\n",
		},
		{
			name:     "libraries install under usr lib",
			expected: "do_install debian/example-libs/usr/lib/example debian/example-libs/usr/lib/example/librenamed.so libexample.so\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Assert(t, cmp.Contains(content, test.expected))
		})
	}
}

func TestDebrootSubPackageOptArtifact(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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
						Description: "Optional tools",
						Artifacts: &dalec.Artifacts{
							Opt: map[string]dalec.ArtifactConfig{
								"opt-tool": {
									SubPath:     "example/bin",
									Permissions: 0o750,
									User:        "tool-user",
									Group:       "tool-group",
									LinuxCapabilities: []dalec.ArtifactCapability{
										{
											Name:      "cap_net_raw",
											Effective: true,
											Permitted: true,
										},
									},
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

	t.Run("the artifact is installed beneath opt in the supplemental package staging directory", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-tools.install"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected example-tools.install to be generated")
		assert.Assert(t, cmp.Contains(
			string(mkfile.Data),
			"do_install debian/example-tools/opt/example/bin debian/example-tools/opt/example/bin/opt-tool opt-tool\n",
		))
	})

	t.Run("the artifact permissions are applied in the supplemental package staging directory", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "dalec/fix_perms.sh"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected fix_perms.sh to be generated")
		assert.Assert(t, cmp.Contains(
			string(mkfile.Data),
			"chmod 750 \"debian/example-tools/opt/example/bin/opt-tool\"\n",
		))
	})

	t.Run("opt permissions enable the fixperms override", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "rules"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected rules to be generated")
		assert.Assert(t, cmp.Contains(string(mkfile.Data), "override_dh_fixperms"))
	})

	t.Run("ownership and capabilities are applied by the supplemental package postinst", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-tools.postinst"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected example-tools.postinst to be generated")
		content := string(mkfile.Data)
		assert.Assert(t, cmp.Contains(content, "chown -R tool-user \"$DESTDIR/opt/example/bin/opt-tool\"\n"))
		assert.Assert(t, cmp.Contains(content, "chgrp -R tool-group \"$DESTDIR/opt/example/bin/opt-tool\"\n"))
		assert.Assert(t, cmp.Contains(content, "setcap 'cap_net_raw=ep' \"$DESTDIR/opt/example/bin/opt-tool\"\n"))
	})
}

func TestDebrootSubPackageCustomName(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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

	ctx := t.Context()
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
							Groups: []dalec.AddGroupConfig{
								{Name: "svcgroup"},
							},
							Binaries: map[string]dalec.ArtifactConfig{
								"svc-bin": {
									Name:    "svc",
									SubPath: "service",
									User:    "svcuser",
									Group:   "svcgroup",
									LinuxCapabilities: []dalec.ArtifactCapability{
										{Name: "cap_net_bind_service", Effective: true, Permitted: true},
									},
								},
								"uncapped": {},
							},
							Directories: &dalec.CreateArtifactDirectories{
								Config: map[string]dalec.ArtifactDirConfig{
									"example": {User: "svcuser", Group: "svcgroup"},
								},
								State: map[string]dalec.ArtifactDirConfig{
									"example": {User: "svcuser", Group: "svcgroup"},
								},
							},
							Links: []dalec.ArtifactSymlinkConfig{
								{
									Source: "/usr/bin/svc",
									Dest:   "/usr/bin/svc-compat",
									User:   "svcuser",
									Group:  "svcgroup",
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

	// Subpackage should produce a postinst file with the resolved name
	mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-svc.postinst"))
	assert.NilError(t, err)
	assert.Assert(t, mkfile != nil, "expected example-svc.postinst to be generated")
	assert.Equal(t, int32(0o700), mkfile.Mode)

	content := string(mkfile.Data)
	tests := []struct {
		name     string
		expected string
	}{
		{name: "debhelper marker is retained", expected: "#DEBHELPER#"},
		{name: "package users are created", expected: "useradd svcuser"},
		{name: "package groups are created", expected: "groupadd --system svcgroup"},
		{name: "artifact user ownership is applied", expected: "chown -R svcuser \"$DESTDIR/usr/bin/service/svc\""},
		{name: "artifact group ownership is applied", expected: "chgrp -R svcgroup \"$DESTDIR/usr/bin/service/svc\""},
		{name: "config directory user ownership is applied", expected: "chown -R svcuser \"$DESTDIR/etc/example\""},
		{name: "config directory group ownership is applied", expected: "chgrp -R svcgroup \"$DESTDIR/etc/example\""},
		{name: "state directory user ownership is applied", expected: "chown -R svcuser \"$DESTDIR/var/lib/example\""},
		{name: "state directory group ownership is applied", expected: "chgrp -R svcgroup \"$DESTDIR/var/lib/example\""},
		{name: "symlink user ownership is applied without dereferencing", expected: "chown -h svcuser \"$DESTDIR/usr/bin/svc-compat\""},
		{name: "symlink group ownership is applied without dereferencing", expected: "chgrp -h svcgroup \"$DESTDIR/usr/bin/svc-compat\""},
		{name: "artifact capabilities are applied", expected: "setcap 'cap_net_bind_service=ep' \"$DESTDIR/usr/bin/service/svc\""},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Assert(t, cmp.Contains(content, test.expected))
		})
	}
}

func TestDebrootSubPackageNoPostinstWhenEmpty(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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

	ctx := t.Context()
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

	ctx := t.Context()
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

	ctx := t.Context()
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
							Directories: &dalec.CreateArtifactDirectories{
								Config: map[string]dalec.ArtifactDirConfig{
									"myapp": {Mode: 0o700},
								},
								State: map[string]dalec.ArtifactDirConfig{
									"myapp": {Mode: 0o750},
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

	t.Run("config directory permissions target the supplemental package staging directory", func(t *testing.T) {
		assert.Assert(t, cmp.Contains(content, "chmod 700 \"debian/example-tools/etc/myapp\"\n"))
	})

	t.Run("state directory permissions target the supplemental package staging directory", func(t *testing.T) {
		assert.Assert(t, cmp.Contains(content, "chmod 750 \"debian/example-tools/var/lib/myapp\"\n"))
	})
}

func TestDebrootSubPackageDocumentationFiles(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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
								"guide.md":  {Name: "renamed-guide.md", SubPath: "guides"},
							},
							Licenses: map[string]dalec.ArtifactConfig{
								"LICENSE": {},
								"COPYING": {Name: "renamed-license", SubPath: "licenses"},
							},
							Manpages: map[string]dalec.ArtifactConfig{
								"example.1":     {},
								"admin.8":       {Name: "renamed.8", SubPath: "admin"},
								"man1/scoped.1": {SubPath: "man1"},
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

	t.Run("docs and licenses without path changes use the package docs file", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-doc.docs"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected example-doc.docs to be generated")
		assert.Assert(t, cmp.Contains(string(mkfile.Data), "README.md\n"))
		assert.Assert(t, cmp.Contains(string(mkfile.Data), "LICENSE\n"))
	})

	t.Run("manpages without path changes use the package manpages file", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-doc.manpages"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected example-doc.manpages to be generated")
		assert.Equal(t, string(mkfile.Data), "example.1\nman1/scoped.1\n")
	})

	t.Run("renamed documentation artifacts install under the supplemental package", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "example-doc.install"))
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected example-doc.install to be generated")

		content := string(mkfile.Data)
		assert.Assert(t, cmp.Contains(content, "do_install debian/example-doc/usr/share/doc/manpages/example-doc/admin debian/example-doc/usr/share/doc/manpages/example-doc/admin/renamed.8 admin.8\n"))
		assert.Assert(t, cmp.Contains(content, "do_install debian/example-doc/usr/share/doc/example-doc/guides debian/example-doc/usr/share/doc/example-doc/guides/renamed-guide.md guide.md\n"))
		assert.Assert(t, cmp.Contains(content, "do_install debian/example-doc/usr/share/doc/example-doc/licenses debian/example-doc/usr/share/doc/example-doc/licenses/renamed-license COPYING\n"))
	})
}

func TestDebrootMultipleSubPackages(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
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

	ctx := t.Context()
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
		ctx := t.Context()
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
		ctx := t.Context()
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
		ctx := t.Context()
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
							Name:        "custom-svc",
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
		assert.Assert(t, cmp.Contains(content, "-pcustom-svc"))
		assert.Assert(t, cmp.Contains(content, "--name=mysvc"))
	})

	t.Run("primary and subpackage units both present", func(t *testing.T) {
		ctx := t.Context()
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
							Name:        "custom-svc",
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
		assert.Assert(t, cmp.Contains(content, "-pcustom-svc --name=subsvc --no-enable"))
	})
}

func TestDebrootSubPackageCustomSystemdPostinst(t *testing.T) {
	t.Parallel()

	t.Run("primary and supplemental packages receive isolated custom postinst partials", func(t *testing.T) {
		ctx := t.Context()
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
						"primary.socket":  {Enable: false},
					},
				},
			},
			Targets: map[string]dalec.Target{
				"testdistro": {
					Packages: map[string]dalec.SubPackage{
						"svc": {
							Name:        "custom-svc",
							Description: "Service package",
							Artifacts: &dalec.Artifacts{
								Systemd: &dalec.SystemdConfiguration{
									Units: map[string]dalec.SystemdUnitConfig{
										"supplemental.service": {Enable: true},
										"supplemental.socket":  {Enable: false},
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

		primary, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "dalec/"+customSystemdPostinstFile))
		assert.NilError(t, err)
		assert.Assert(t, primary != nil, "expected a primary custom systemd postinst partial")
		assert.Assert(t, cmp.Contains(string(primary.Data), "primary.service"))
		assert.Assert(t, !bytes.Contains(primary.Data, []byte("supplemental.service")))

		supplemental, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "dalec/custom-svc."+customSystemdPostinstFile))
		assert.NilError(t, err)
		assert.Assert(t, supplemental != nil, "expected a supplemental custom systemd postinst partial")
		assert.Assert(t, cmp.Contains(string(supplemental.Data), "supplemental.service"))
		assert.Assert(t, !bytes.Contains(supplemental.Data, []byte("primary.service")))

		rules, err := findMkfile(t, def.ToPB(), filepath.Join("/debian", "rules"))
		assert.NilError(t, err)
		assert.Assert(t, rules != nil, "expected Debian rules to be generated")
		assert.Assert(t, cmp.Contains(string(rules.Data), "cat debian/dalec/"+customSystemdPostinstFile+" >> debian/postinst"))
		assert.Assert(t, cmp.Contains(string(rules.Data), "cat debian/dalec/custom-svc."+customSystemdPostinstFile+" >> debian/custom-svc.postinst"))
	})

	t.Run("a subpackage enables its service without enabling its socket", func(t *testing.T) {
		ctx := t.Context()
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

		// The subpackage's custom-enable snippet must be written to its own
		// per-package partial (keyed by resolved name), not the primary partial,
		// so it can be appended to the subpackage's own maintainer script.
		subPartial := filepath.Join("/debian", "dalec/example-svc."+customSystemdPostinstFile)
		mkfile, err := findMkfile(t, def.ToPB(), subPartial)
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected per-subpackage custom systemd postinst partial to be generated for subpackage with mixed enable")

		content := string(mkfile.Data)
		assert.Assert(t, cmp.Contains(content, "foo.service"))
		assert.Assert(t, cmp.Contains(content, "foo.socket"))

		// The primary partial must NOT be generated since the primary package has
		// no units; the subpackage's units must not leak into it.
		primaryPartial := filepath.Join("/debian", "dalec/"+customSystemdPostinstFile)
		primaryFile, err := findMkfile(t, def.ToPB(), primaryPartial)
		assert.NilError(t, err)
		assert.Assert(t, primaryFile == nil, "subpackage units must not be routed into the primary custom systemd postinst partial")
	})

	t.Run("a subpackage only enabling its service does not generate a custom postinst", func(t *testing.T) {
		ctx := t.Context()
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

	ctx := t.Context()
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
