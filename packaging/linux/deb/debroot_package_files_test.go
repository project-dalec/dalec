package deb

import (
	"fmt"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	testutil "github.com/project-dalec/dalec/internal/test"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestDebrootPackageFiles(t *testing.T) {
	t.Parallel()

	artifacts := dalec.Artifacts{
		Binaries: map[string]dalec.ArtifactConfig{
			"example-bin": {
				Permissions: 0o750,
				User:        "example-user",
				Group:       "example-group",
				LinuxCapabilities: []dalec.ArtifactCapability{
					{Name: "cap_net_bind_service", Effective: true, Permitted: true},
				},
			},
		},
		ConfigFiles: map[string]dalec.ArtifactConfig{
			"example.conf": {SubPath: "example", Permissions: 0o740, User: "example-user"},
		},
		Manpages: map[string]dalec.ArtifactConfig{
			"example.1": {Permissions: 0o740, User: "example-user"},
		},
		Headers: map[string]dalec.ArtifactConfig{
			"example.h": {SubPath: "example", Permissions: 0o740, User: "example-user"},
		},
		Licenses: map[string]dalec.ArtifactConfig{
			"LICENSE": {Permissions: 0o740, User: "example-user"},
		},
		Docs: map[string]dalec.ArtifactConfig{
			"README.md": {Permissions: 0o740, User: "example-user"},
		},
		Libs: map[string]dalec.ArtifactConfig{
			"libexample.so": {
				SubPath:     "example",
				Permissions: 0o740,
				User:        "example-user",
				LinuxCapabilities: []dalec.ArtifactCapability{
					{Name: "cap_sys_chroot", Effective: true, Permitted: true},
				},
			},
		},
		Libexec: map[string]dalec.ArtifactConfig{
			"example-helper": {
				SubPath:     "example",
				Permissions: 0o740,
				User:        "example-user",
				LinuxCapabilities: []dalec.ArtifactCapability{
					{Name: "cap_sys_ptrace", Effective: true, Permitted: true},
				},
			},
		},
		DataDirs: map[string]dalec.ArtifactConfig{
			"example.dat": {SubPath: "example", Permissions: 0o740, User: "example-user"},
		},
		Directories: &dalec.CreateArtifactDirectories{
			Config: map[string]dalec.ArtifactDirConfig{
				"example": {Mode: 0o750, User: "example-user", Group: "example-group"},
			},
			State: map[string]dalec.ArtifactDirConfig{
				"example": {Mode: 0o700, User: "example-user", Group: "example-group"},
			},
		},
		Systemd: &dalec.SystemdConfiguration{
			Units: map[string]dalec.SystemdUnitConfig{
				"example.service": {Enable: true},
			},
			Dropins: map[string]dalec.SystemdDropinConfig{
				"override.conf": {Unit: "example.service"},
			},
		},
		Links: []dalec.ArtifactSymlinkConfig{
			{
				Source: "/usr/bin/example-bin",
				Dest:   "/usr/bin/example-compat",
				User:   "example-user",
				Group:  "example-group",
			},
		},
		Users:  []dalec.AddUserConfig{{Name: "example-user"}},
		Groups: []dalec.AddGroupConfig{{Name: "example-group"}},
	}
	subArtifacts := artifacts
	spec := &dalec.Spec{
		Name:        "example",
		Description: "Primary package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "Apache-2.0",
		Artifacts:   artifacts,
		Targets: map[string]dalec.Target{
			"testdistro": {
				Packages: map[string]dalec.SubPackage{
					"extra": {
						Name:        "custom-extra",
						Description: "Supplemental package",
						Artifacts:   &subArtifacts,
					},
				},
			},
		},
	}

	ctx := t.Context()
	st := Debroot(ctx, dalec.SourceOpts{}, spec, llb.Scratch(), llb.Scratch(), "testdistro", "", "", SourcePkgConfig{})
	def, err := st.Marshal(ctx)
	assert.NilError(t, err)

	var execCommands []string
	for _, op := range testutil.LLBOpsFromState(ctx, t, st) {
		exec := op.Op.GetExec()
		if exec != nil {
			execCommands = append(execCommands, strings.Join(exec.Meta.Args, " "))
		}
	}
	allExecCommands := strings.Join(execCommands, "\n")

	packages := []struct {
		name            string
		postinst        string
		systemdLinkName string
	}{
		{name: "example", postinst: "postinst", systemdLinkName: "example.service"},
		{name: "custom-extra", postinst: "custom-extra.postinst", systemdLinkName: "custom-extra.example.service"},
	}

	for _, pkg := range packages {
		t.Run(pkg.name+" emits every package-local file", func(t *testing.T) {
			files := []struct {
				name     string
				mode     int32
				expected []string
			}{
				{
					name: pkg.name + ".install",
					mode: 0o700,
					expected: []string{
						"debian/" + pkg.name + "/usr/bin/example-bin",
						"debian/" + pkg.name + "/etc/example/example.conf",
						"debian/" + pkg.name + "/usr/include/example/example.h",
						"debian/" + pkg.name + "/lib/systemd/system/example.service.d/override.conf",
						"debian/" + pkg.name + "/usr/share/example/example.dat",
						"debian/" + pkg.name + "/usr/libexec/example/example-helper",
						"debian/" + pkg.name + "/usr/lib/example/libexample.so",
					},
				},
				{
					name:     pkg.name + ".dirs",
					mode:     0o640,
					expected: []string{"/etc/example\n", "/var/lib/example\n"},
				},
				{
					name:     pkg.name + ".docs",
					mode:     0o640,
					expected: []string{"README.md\n", "LICENSE\n"},
				},
				{
					name:     pkg.name + ".manpages",
					mode:     0o640,
					expected: []string{"example.1\n"},
				},
				{
					name:     pkg.name + ".links",
					mode:     0o644,
					expected: []string{"usr/bin/example-bin usr/bin/example-compat\n"},
				},
				{
					name: pkg.postinst,
					mode: 0o700,
					expected: []string{
						"useradd example-user",
						"groupadd --system example-group",
						"setcap 'cap_net_bind_service=ep' \"$DESTDIR/usr/bin/example-bin\"",
						"setcap 'cap_sys_chroot=ep' \"$DESTDIR/usr/lib/example/libexample.so\"",
						"setcap 'cap_sys_ptrace=ep' \"$DESTDIR/usr/libexec/example/example-helper\"",
						"chown -R example-user \"$DESTDIR/usr/bin/example-bin\"",
						"chown -R example-user \"$DESTDIR/etc/example/example.conf\"",
						"chown -R example-user \"$DESTDIR/usr/include/example/example.h\"",
						"chown -R example-user \"$DESTDIR/usr/lib/example/libexample.so\"",
						"chown -R example-user \"$DESTDIR/usr/libexec/example/example-helper\"",
						"chown -R example-user \"$DESTDIR/usr/share/example/example.dat\"",
						"chown -h example-user \"$DESTDIR/usr/bin/example-compat\"",
						"chown -R example-user \"$DESTDIR/usr/share/doc/manpages/" + pkg.name + "/example.1\"",
						"chown -R example-user \"$DESTDIR/usr/share/doc/" + pkg.name + "/LICENSE\"",
						"chown -R example-user \"$DESTDIR/usr/share/doc/" + pkg.name + "/README.md\"",
					},
				},
			}

			for _, file := range files {
				t.Run(file.name, func(t *testing.T) {
					mkfile, err := findMkfile(t, def.ToPB(), "/debian/"+file.name)
					assert.NilError(t, err)
					assert.Assert(t, mkfile != nil, "expected %s to be generated", file.name)
					assert.Equal(t, mkfile.Mode, file.mode)
					for _, expected := range file.expected {
						assert.Assert(t, cmp.Contains(string(mkfile.Data), expected))
					}
				})
			}

			t.Run("systemd unit symlink is package-local", func(t *testing.T) {
				expected := fmt.Sprintf("ln -s ../example.service %s", pkg.systemdLinkName)
				assert.Assert(t, cmp.Contains(allExecCommands, expected))
			})
		})
	}

	t.Run("permission fixups include every package staging root", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), "/debian/dalec/fix_perms.sh")
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected fix_perms.sh to be generated")

		content := string(mkfile.Data)
		for _, pkg := range packages {
			expected := []string{
				fmt.Sprintf("chmod 750 \"debian/%s/usr/bin/example-bin\"", pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/etc/example/example.conf\"", pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/share/doc/manpages/%s/example.1\"", pkg.name, pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/include/example/example.h\"", pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/share/doc/%s/LICENSE\"", pkg.name, pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/share/doc/%s/README.md\"", pkg.name, pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/lib/example/libexample.so\"", pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/libexec/example/example-helper\"", pkg.name),
				fmt.Sprintf("chmod 740 \"debian/%s/usr/share/example/example.dat\"", pkg.name),
				fmt.Sprintf("chmod 750 \"debian/%s/etc/example\"", pkg.name),
				fmt.Sprintf("chmod 700 \"debian/%s/var/lib/example\"", pkg.name),
			}

			for _, command := range expected {
				assert.Assert(t, cmp.Contains(content, command))
			}
		}
	})

	t.Run("multiple packages emit one override per debhelper command", func(t *testing.T) {
		mkfile, err := findMkfile(t, def.ToPB(), "/debian/rules")
		assert.NilError(t, err)
		assert.Assert(t, mkfile != nil, "expected Debian rules to be generated")

		content := string(mkfile.Data)
		assert.Equal(t, strings.Count(content, "override_dh_fixperms:"), 1)
		assert.Equal(t, strings.Count(content, "override_dh_installsystemd:"), 1)
	})
}
