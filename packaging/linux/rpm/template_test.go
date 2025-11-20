package rpm

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/project-dalec/dalec"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestTemplateSources(t *testing.T) {
	t.Run("no sources", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{}}
		s, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expect := ""
		actual := s.String()
		assert.Equal(t, actual, expect)
	})

	// Each source entry is prefixed by comments documenting how the source was generated
	// This gets the source documentation and turns it into the expected comment string
	srcDoc := func(name string, src dalec.Source) string {
		rdr := src.Doc(name)
		buf := bytes.NewBuffer(nil)
		scanner := bufio.NewScanner(rdr)
		for scanner.Scan() {
			buf.WriteString("# ")
			buf.WriteString(scanner.Text())
			buf.WriteString("\n")
		}
		return buf.String()
	}

	t.Run("one source file", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{},
					},
				},
			},
		}}

		out, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedDoc := srcDoc("src1", w.Spec.Sources["src1"])

		s := out.String()
		if !strings.HasPrefix(s, expectedDoc) {
			t.Errorf("Expected doc:\n%q\n\n, got:\n%q\n", expectedDoc, s)
		}

		// File sources are not (currently) compressed, so the source is the file itself
		expected := "Source0: src1\n\n"
		actual := s[len(expectedDoc):] // trim off the doc from the output
		if actual != expected {
			t.Fatalf("unexpected sources: expected %q, got: %q", expected, actual)
		}
	})

	t.Run("one source dir", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
				},
			},
		}}

		out, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedDoc := srcDoc("src1", w.Spec.Sources["src1"])

		s := out.String()
		if !strings.HasPrefix(s, expectedDoc) {
			t.Errorf("Expected doc:\n%q\n\n, got:\n%q\n", expectedDoc, s)
		}

		expected := "Source0: src1.tar.gz\n\n"
		actual := s[len(expectedDoc):] // trim off the doc from the output
		if actual != expected {
			t.Fatalf("unexpected sources: expected %q, got: %q", expected, actual)
		}

		t.Run("with gomod", func(t *testing.T) {
			src := w.Spec.Sources["src1"]
			src.Generate = []*dalec.SourceGenerator{
				{Gomod: &dalec.GeneratorGomod{}},
			}
			w.Spec.Sources["src1"] = src

			out2, err := w.Sources()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			s2 := out2.String()
			// trim last newline from the first output since that has shifted
			s3 := s[:len(s)-1]
			if !strings.HasPrefix(s2, s3) {
				t.Fatalf("expected output to start with %q, got %q", s3, out2.String())
			}

			s2 = strings.TrimPrefix(out2.String(), s3)
			expected := "Source1: " + gomodsName + ".tar.gz\n\n"
			if s2 != expected {
				t.Fatalf("unexpected sources: expected %q, got: %q", expected, s2)
			}
		})

		t.Run("with cargohome", func(t *testing.T) {
			src := w.Spec.Sources["src1"]
			src.Generate = []*dalec.SourceGenerator{
				{Cargohome: &dalec.GeneratorCargohome{}},
			}
			w.Spec.Sources["src1"] = src

			out2, err := w.Sources()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			s2 := out2.String()
			// trim last newline from the first output since that has shifted
			s3 := s[:len(s)-1]
			if !strings.HasPrefix(s2, s3) {
				t.Fatalf("expected output to start with %q, got %q", s, out2.String())
			}

			s2 = strings.TrimPrefix(out2.String(), s3)
			expected := "Source1: " + cargohomeName + ".tar.gz\n\n"
			if s2 != expected {
				t.Fatalf("unexpected sources: expected %q, got: %q", expected, s2)
			}
		})

		t.Run("with pip", func(t *testing.T) {
			src := w.Spec.Sources["src1"]
			src.Generate = []*dalec.SourceGenerator{
				{Pip: &dalec.GeneratorPip{}},
			}
			w.Spec.Sources["src1"] = src

			out2, err := w.Sources()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			s2 := out2.String()
			if s2 != s {
				t.Fatalf("expected no additional sources for pip, got: %q", s2)
			}
		})

	})

	t.Run("multiple sources", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{},
					},
				},
				"src4": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
					Generate: []*dalec.SourceGenerator{
						{Gomod: &dalec.GeneratorGomod{}},
					},
				},
				"src5": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
					Generate: []*dalec.SourceGenerator{
						{Gomod: &dalec.GeneratorGomod{}},
					},
				},
				"src6": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
					Generate: []*dalec.SourceGenerator{
						{Cargohome: &dalec.GeneratorCargohome{}},
					},
				},
				"src7": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
					Generate: []*dalec.SourceGenerator{
						{Pip: &dalec.GeneratorPip{}},
					},
				},
			},
		}}

		out, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		s := out.String()

		// Note: order (in the produced output) should be deterministic here regardless of map ordering (especially since maps are randomized).
		ordered := dalec.SortMapKeys(w.Spec.Sources)
		for i, name := range ordered {
			src := w.Spec.Sources[name]
			expectedDoc := srcDoc(name, src)

			if !strings.HasPrefix(s, expectedDoc) {
				t.Errorf("%s: Expected doc:\n%q\n\n, got:\n%q\n", name, expectedDoc, s)
			}

			s = s[len(expectedDoc):] // trim off the doc from the output
			suffix := "\n"
			if dalec.SourceIsDir(src) {
				suffix = ".tar.gz\n"
			}

			expected := "Source" + strconv.Itoa(i) + ": " + name + suffix
			if s[:len(expected)] != expected {
				t.Fatalf("%s: unexpected sources: expected %q, got: %q", name, expected, s[:len(expected)])
			}

			// Trim off the rest of the bits we've checked for the next loop iteration
			s = s[len(expected):]
		}

		// Now we should have entries for gomods and cargohome.
		// Note there are 2 gomod sources but they should be combined into one entry.
		// Pip no longer creates a separate cache source.

		expected := "Source7: " + gomodsName + ".tar.gz\nSource8: " + cargohomeName + ".tar.gz\n\n"
		if s != expected {
			t.Fatalf("generators: unexpected sources: expected %q, got: %q", expected, s)
		}
		s = s[len(expected):]
		if s != "" {
			t.Fatalf("unexpected trailing sources: %q", s)
		}
	})
}

func TestTemplate_Artifacts(t *testing.T) {

	t.Run("test systemd post", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"test1.service": {},
						"test2.service": {
							Enable: true,
						},
						"test3.service": {
							Enable: true,
							Start: true,
						},
					},
				},
			},
		}}

		assert.Equal(t, w.Post().String(),
			`%post

if [ $1 -eq 1 ]; then
    # initial installation
    systemctl enable test2.service
fi

if [ $1 -eq 1 ]; then
    # initial installation
    systemctl enable --now test3.service
fi

`)
	})

	t.Run("test systemd post, no enabled units", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"test1.service": {},
						"test2.service": {},
					},
				},
			},
		}}

		assert.Equal(t, w.Post().String(), ``)
	})

	t.Run("test systemd preun", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"test1.service": {},
						"test2.service": {
							Enable: true,
						},
						"test3.service": {
							Enable: true,
							Start: true,
						},
					},
				},
			},
		}}

		assert.Equal(t, w.PreUn().String(),
			`%preun

if [ $1 -eq 0 ]; then
    # complete uninstallation
    systemctl disable --now test2.service
fi

if [ $1 -eq 0 ]; then
    # complete uninstallation
    systemctl disable --now test3.service
fi
`)
	})

	t.Run("test systemd preun, no enabled units", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"test1.service": {},
						"test2.service": {},
					},
				},
			},
		}}

		assert.Equal(t, w.PreUn().String(), ``)
	})

	t.Run("test systemd unit postun", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"test.service": {},
					},
				},
			},
		}}

		got := w.PostUn().String()
		want := `%postun
%systemd_postun test.service
`
		assert.Equal(t, want, got)
	})

	t.Run("test doc templating using artifact config", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"README.md": {
						SubPath: "docs",
						Name:    "README",
					},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%doc %{_docdir}/test-pkg/docs/README

`

		assert.Equal(t, want, got)
	})

	t.Run("test doc templating using defaults", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"README.md": {},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%doc %{_docdir}/test-pkg/README.md

`
		assert.Equal(t, want, got)
	})

	t.Run("test doc templating using defaults and longer path", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"/some/path/to/README.md": {},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%doc %{_docdir}/test-pkg/README.md

`
		assert.Equal(t, want, got)
	})

	t.Run("test license templating using defaults", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Licenses: map[string]dalec.ArtifactConfig{
					"LICENSE": {},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%license %{_licensedir}/test-pkg/LICENSE

`
		assert.Equal(t, want, got)
	})

	t.Run("test license templating using ArtifactConfig", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Licenses: map[string]dalec.ArtifactConfig{
					"LICENSE": {
						Name:    "LICENSE.md",
						SubPath: "licenses",
					},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%license %{_licensedir}/test-pkg/licenses/LICENSE.md

`
		assert.Equal(t, want, got)
	})

	t.Run("test headers templating using defaults", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Headers: map[string]dalec.ArtifactConfig{
					"test-headers": {},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%{_includedir}/test-headers

`
		assert.Equal(t, want, got)
	})

	t.Run("test headers templating using ArtifactConfig", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				Headers: map[string]dalec.ArtifactConfig{
					"test-headers": {
						Name:    "sub-module-headers",
						SubPath: "sub-module",
					},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%{_includedir}/sub-module/sub-module-headers

`
		assert.Equal(t, want, got)
	})

	t.Run("test config file templating using ArtifactConfig", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"/src/config.env": {
						Name:    "config",
						SubPath: "sysconfig",
						User:    "myuser",
						Group:   "mygroup",
					},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%config(noreplace) %{_sysconfdir}/sysconfig/config

`
		assert.Equal(t, want, got)
	})

	t.Run("test config file templating using defaults", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-pkg",
			Artifacts: dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"/src/config.env": {},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%config(noreplace) %{_sysconfdir}/config.env

`
		assert.Equal(t, want, got)
	})

	t.Run("test systemd dropin templating", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Dropins: map[string]dalec.SystemdDropinConfig{
						"src/blah.config": {
							Unit: "foo.service",
						},
					},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%dir %{_unitdir}/foo.service.d
%{_unitdir}/foo.service.d/blah.config

`
		assert.Equal(t, want, got)
	})

	t.Run("test systemd dropin templating two files and mixed config", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Dropins: map[string]dalec.SystemdDropinConfig{
						"src/blah.config": {
							Unit: "foo.service",
						},
						"src/env.config": {
							Unit: "foo.service",
							Name: "test.conf",
						},
					},
				},
			},
		}}

		got := w.Files().String()
		want := `%files
%dir %{_unitdir}/foo.service.d
%{_unitdir}/foo.service.d/blah.config
%{_unitdir}/foo.service.d/test.conf

`
		assert.Equal(t, want, got)
	})

	t.Run("test systemd artifact installed under a different name", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/project-dalec/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{

							Files: map[string]*dalec.SourceInlineFile{
								"simple.service": {
									Contents: `
Phony unit
`},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"src/simple.service": {
							Enable: true,
							Name:   "phony.service",
						},
					},
				},
			},
		}
		w := specWrapper{Spec: spec}

		assert.Equal(t, w.Install().String(), `%install
mkdir -p %{buildroot}/%{_unitdir}
cp -r src/simple.service %{buildroot}/%{_unitdir}/phony.service

`)

		assert.Equal(t, w.Files().String(), `%files
%{_unitdir}/phony.service

`)
	})

	t.Run("test user", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Users: []dalec.AddUserConfig{
					{Name: "testuser"},
				},
			},
		}}

		got := w.Post().String()
		want := `%post
getent passwd testuser >/dev/null || adduser testuser

`

		assert.Equal(t, want, got)
	})

	t.Run("test group", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Groups: []dalec.AddGroupConfig{
					{Name: "testgroup"},
				},
			},
		}}

		got := w.Post().String()
		want := `%post
getent group testgroup >/dev/null || groupadd --system testgroup

`

		assert.Equal(t, want, got)
	})

	t.Run("disable auto requires", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				DisableAutoRequires: true,
			},
		}}

		got := w.DisableAutoReq()
		want := "AutoReq: no"
		assert.Equal(t, want, got)

		w = &specWrapper{Spec: &dalec.Spec{}}
		got = w.DisableAutoReq()
		want = ""
		assert.Equal(t, want, got)
	})

	t.Run("custom package files for rpm", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-package",
			Artifacts: dalec.Artifacts{
				PackageFiles: map[string]string{
					"rpm": `%{_bindir}/myapp
%{_includedir}/myapp.h
%{_mandir}/man1/myapp.1*`,
				},
			},
		}}

		// Test Files() method uses custom listing
		got := w.Files().String()
		want := `%files
%{_bindir}/myapp
%{_includedir}/myapp.h
%{_mandir}/man1/myapp.1*

`
		assert.Equal(t, want, got)

		// Test Install() method skips individual artifact copying
		installGot := w.Install().String()
		installWant := `%install

`
		assert.Equal(t, installWant, installGot)
	})

	t.Run("custom package files with symlinks", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-package",
			Artifacts: dalec.Artifacts{
				PackageFiles: map[string]string{
					"rpm": `%{_bindir}/myapp
%{_includedir}/myapp.h`,
				},
				Links: []dalec.ArtifactSymlinkConfig{
					{
						Source: "/usr/bin/myapp",
						Dest:   "/usr/local/bin/myapp-link",
					},
				},
			},
		}}

		// Test Install() method still handles symlinks
		installGot := w.Install().String()
		installWant := `%install
mkdir -p %{buildroot}/usr/local/bin
ln -sf /usr/bin/myapp %{buildroot}//usr/local/bin/myapp-link

`
		assert.Equal(t, installWant, installGot)
	})

	t.Run("fallback to traditional artifacts when no custom files", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Name: "test-package",
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"myapp": {},
				},
			},
		}}

		// Test Files() method falls back to traditional behavior
		got := w.Files().String()
		assert.Assert(t, strings.Contains(got, "%{_bindir}/myapp"))

		// Test Install() method uses traditional copying
		installGot := w.Install().String()
		assert.Assert(t, strings.Contains(installGot, "mkdir -p %{buildroot}/%{_bindir}"))
		assert.Assert(t, strings.Contains(installGot, "cp -r myapp"))
	})
}

func TestTemplate_Requires(t *testing.T) {
	t.Parallel()

	spec := &dalec.Spec{
		Dependencies: &dalec.PackageDependencies{
			// note: I've prefixed these packages with a/b/c for sorting purposes
			// Since the underlying code will sort packages this just makes it
			// simpler to read for tests.
			Build: map[string]dalec.PackageConstraints{
				"a-lib-no-constraints": {},
				"b-lib-one-constraints": {
					Version: []string{"< 2.0"},
				},
				"c-lib-multiple-constraints": {
					Version: []string{
						"< 2.0",
						">= 1.0",
					},
				},
				"d-lib-single-arch-constraints": {
					Arch: []string{"arm64"},
				},
				"e-lib-multi-arch-constraints": {
					Arch: []string{"amd64", "arm64"},
				},
				"f-lib-multi-arch-multi-version-constraints": {
					Arch:    []string{"amd64", "arm64"},
					Version: []string{"< 2.0", ">= 1.0"},
				},
			},
			Runtime: map[string]dalec.PackageConstraints{
				"a-no-constraints": {},
				"b-one-constraints": {
					Version: []string{"< 2.0"},
				},
				"c-multiple-constraints": {
					Version: []string{
						"< 2.0",
						">= 1.0",
					},
				},
				"d-single-arch-constraints": {
					Arch: []string{"arm64"},
				},
				"e-multi-arch-constraints": {
					Arch: []string{"amd64", "arm64"},
				},
				"f-multi-arch-multi-version-constraints": {
					Arch:    []string{"amd64", "arm64"},
					Version: []string{"< 2.0", ">= 1.0"},
				},
			},
		},
	}

	w := &specWrapper{Spec: spec}

	got := w.Requires().String()
	want := `BuildRequires: a-lib-no-constraints
BuildRequires: b-lib-one-constraints < 2.0
BuildRequires: c-lib-multiple-constraints < 2.0
BuildRequires: c-lib-multiple-constraints >= 1.0
%ifarch arm64
BuildRequires: d-lib-single-arch-constraints
%endif
%ifarch amd64
BuildRequires: e-lib-multi-arch-constraints
%endif
%ifarch arm64
BuildRequires: e-lib-multi-arch-constraints
%endif
%ifarch amd64
BuildRequires: f-lib-multi-arch-multi-version-constraints < 2.0
BuildRequires: f-lib-multi-arch-multi-version-constraints >= 1.0
%endif
%ifarch arm64
BuildRequires: f-lib-multi-arch-multi-version-constraints < 2.0
BuildRequires: f-lib-multi-arch-multi-version-constraints >= 1.0
%endif

Requires: a-no-constraints
Requires: b-one-constraints < 2.0
Requires: c-multiple-constraints < 2.0
Requires: c-multiple-constraints >= 1.0
%ifarch arm64
Requires: d-single-arch-constraints
%endif
%ifarch amd64
Requires: e-multi-arch-constraints
%endif
%ifarch arm64
Requires: e-multi-arch-constraints
%endif
%ifarch amd64
Requires: f-multi-arch-multi-version-constraints < 2.0
Requires: f-multi-arch-multi-version-constraints >= 1.0
%endif
%ifarch arm64
Requires: f-multi-arch-multi-version-constraints < 2.0
Requires: f-multi-arch-multi-version-constraints >= 1.0
%endif

`

	assert.Equal(t, want, got)
}

func TestTemplateOptionalFields(t *testing.T) {
	spec := &dalec.Spec{
		Name:        "testing",
		Version:     "0.0.1",
		Revision:    "1",
		Description: "A helpful tool",
		License:     "MIT",
	}

	w := &strings.Builder{}
	err := specTmpl.Execute(w, &specWrapper{Spec: spec})
	assert.NilError(t, err)

	actual := strings.TrimSpace(w.String())
	expect := strings.TrimSpace(`
Name: testing
Version: 0.0.1
Release: 1%{?dist}
License: MIT
Summary: A helpful tool

%description
A helpful tool

%install

%files
`)

	assert.Equal(t, expect, actual)

	w.Reset()

	spec.Packager = "Awesome Packager"
	err = specTmpl.Execute(w, &specWrapper{Spec: spec})
	assert.NilError(t, err)

	actual = strings.TrimSpace(w.String())
	expect = strings.TrimSpace(`
Name: testing
Version: 0.0.1
Release: 1%{?dist}
License: MIT
Summary: A helpful tool

Packager: Awesome Packager
%description
A helpful tool

%install

%files

`)

	defer func() {
		if t.Failed() {
			t.Log(actual)
		}
	}()
	assert.Equal(t, expect, actual)
}

func TestTemplate_ImplicitRequires(t *testing.T) {
	t.Run("systemd", func(t *testing.T) {
		spec := &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"test.service": {
							Enable: true,
						},
					},
				},
			},
		}

		w := specWrapper{Spec: spec}

		got := w.Requires().String()
		assert.Equal(t, got,
			`Requires(post): systemd
Requires(preun): systemd
Requires(postun): systemd
OrderWithRequires(post): systemd
OrderWithRequires(preun): systemd
OrderWithRequires(postun): systemd
`,
		)

		spec.Artifacts.Systemd.Units = map[string]dalec.SystemdUnitConfig{
			"test.service": {
				Enable: false,
			},
		}

		got = w.Requires().String()
		assert.Equal(t, got,
			`Requires(preun): systemd
Requires(postun): systemd
OrderWithRequires(preun): systemd
OrderWithRequires(postun): systemd
`)
	})

	t.Run("user", func(t *testing.T) {
		spec := &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Users: []dalec.AddUserConfig{
					{Name: "testuser"},
				},
			},
		}

		w := specWrapper{Spec: spec}

		got := w.Requires().String()
		want := "Requires(post): /usr/sbin/adduser, /usr/bin/getent\n"
		assert.Equal(t, got, want)
	})

	t.Run("group", func(t *testing.T) {
		spec := &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Groups: []dalec.AddGroupConfig{
					{Name: "testgroup"},
				},
			},
		}

		w := specWrapper{Spec: spec}

		got := w.Requires().String()
		want := "Requires(post): /usr/sbin/groupadd, /usr/bin/getent\n"
		assert.Equal(t, got, want)
	})
}

func TestTemplate_DisableStrip(t *testing.T) {
	spec := &dalec.Spec{
		Artifacts: dalec.Artifacts{
			DisableStrip: true,
		},
	}

	w := &specWrapper{Spec: spec}
	want := `%global __strip /bin/true`
	got := w.DisableStrip()
	assert.Equal(t, got, want)

	spec.Artifacts.DisableStrip = false
	want = ""
	got = w.DisableStrip()
	assert.Equal(t, got, want)
}

func TestTemplate_Provides(t *testing.T) {
	spec := &dalec.Spec{
		Provides: map[string]dalec.PackageConstraints{
			"test-provides": {},
		},
	}

	w := &specWrapper{Spec: spec}
	got := w.Provides().String()
	want := "Provides: test-provides\n\n"
	assert.Equal(t, got, want)

	w.Spec.Provides["test-provides"] = dalec.PackageConstraints{
		Version: []string{"= 2.0.0"},
	}
	got = w.Provides().String()
	want = "Provides: test-provides == 2.0.0\n\n"
	assert.Equal(t, got, want)
}

func TestTemplate_Replaces(t *testing.T) {
	spec := &dalec.Spec{
		Replaces: map[string]dalec.PackageConstraints{
			"test-replaces": {},
		},
	}

	w := &specWrapper{Spec: spec}
	got := w.Replaces().String()
	want := "Obsoletes: test-replaces\n"
	assert.Equal(t, got, want)

	w.Spec.Replaces["test-replaces"] = dalec.PackageConstraints{
		Version: []string{"< 2.0.0"},
	}
	got = w.Replaces().String()
	want = "Obsoletes: test-replaces < 2.0.0\n"
	assert.Equal(t, got, want)
}

func TestTemplate_TargetSpecificOverrides(t *testing.T) {
	// Create a spec with both root-level and target-specific values
	spec := &dalec.Spec{
		Name:    "test-pkg",
		Version: "1.0.0",
		// Root-level definitions
		Replaces: map[string]dalec.PackageConstraints{
			"root-pkg-r": {Version: []string{">= 1.0.0"}},
			"common-pkg": {Version: []string{">= 2.0.0"}}, // Will be overridden in target1
		},
		Conflicts: map[string]dalec.PackageConstraints{
			"root-pkg-c": {Version: []string{"<= 3.0.0"}},
			"common-pkg": {Version: []string{"<= 4.0.0"}}, // Will be overridden in target1
		},
		Provides: map[string]dalec.PackageConstraints{
			"root-pkg-p": {Version: []string{"= 5.0.0"}},
			"common-pkg": {Version: []string{"= 6.0.0"}}, // Will be overridden in target1
		},
		Targets: map[string]dalec.Target{
			// target1 overrides values
			"target1": {
				Artifacts: &dalec.Artifacts{
					DisableAutoRequires: true,
				},
				Replaces: map[string]dalec.PackageConstraints{
					"target-pkg-r": {Version: []string{">= 1.1.0"}},
					"common-pkg":   {Version: []string{">= 2.1.0"}}, // Overrides root
				},
				Conflicts: map[string]dalec.PackageConstraints{
					"target-pkg-c": {Version: []string{"<= 3.1.0"}},
					"common-pkg":   {Version: []string{"<= 4.1.0"}}, // Overrides root
				},
				Provides: map[string]dalec.PackageConstraints{
					"target-pkg-p": {Version: []string{"= 5.1.0"}},
					"common-pkg":   {Version: []string{"= 6.1.0"}}, // Overrides root
				},
			},
			// target2 has explicit empty maps to override the root values
			"target2": {
				Replaces:  map[string]dalec.PackageConstraints{},
				Conflicts: map[string]dalec.PackageConstraints{},
				Provides:  map[string]dalec.PackageConstraints{},
			},
		},
	}

	t.Run("target1 should override root values", func(t *testing.T) {
		w := &specWrapper{Spec: spec, Target: "target1"}

		// Test Replaces - should contain target-specific values and not root values for common-pkg
		replaces := w.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "Obsoletes: common-pkg >= 2.1.0"))
		assert.Assert(t, cmp.Contains(replaces, "Obsoletes: target-pkg-r >= 1.1.0"))
		assert.Assert(t, !strings.Contains(replaces, "root-pkg-r"))
		assert.Assert(t, !strings.Contains(replaces, ">= 2.0.0")) // common-pkg old version

		// Test Conflicts - should contain target-specific values and not root values for common-pkg
		conflicts := w.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: common-pkg <= 4.1.0"))
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: target-pkg-c <= 3.1.0"))
		assert.Assert(t, !strings.Contains(conflicts, "root-pkg-c"))
		assert.Assert(t, !strings.Contains(conflicts, "<= 4.0.0")) // common-pkg old version

		// Test Provides - should contain target-specific values and not root values for common-pkg
		provides := w.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "Provides: common-pkg == 6.1.0"))
		assert.Assert(t, cmp.Contains(provides, "Provides: target-pkg-p == 5.1.0"))
		assert.Assert(t, !strings.Contains(provides, "root-pkg-p"))
		assert.Assert(t, !strings.Contains(provides, "= 6.0.0")) // common-pkg old version

		assert.Assert(t, cmp.Equal(w.DisableAutoReq(), "AutoReq: no"))
	})

	t.Run("target2 should use empty maps", func(t *testing.T) {
		w := &specWrapper{Spec: spec, Target: "target2"}

		// Test Replaces - should be empty
		assert.Equal(t, w.Replaces().String(), "")

		// Test Conflicts - should be empty
		assert.Equal(t, w.Conflicts().String(), "")

		// Test Provides - should be empty
		assert.Equal(t, w.Provides().String(), "")
	})

	t.Run("non-existent target should use root values", func(t *testing.T) {
		w := &specWrapper{Spec: spec, Target: "non-existent-target"}

		// Test Replaces - should contain root values
		replaces := w.Replaces().String()
		assert.Assert(t, cmp.Contains(replaces, "Obsoletes: common-pkg >= 2.0.0"))
		assert.Assert(t, cmp.Contains(replaces, "Obsoletes: root-pkg-r >= 1.0.0"))

		// Test Conflicts - should contain root values
		conflicts := w.Conflicts().String()
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: common-pkg <= 4.0.0"))
		assert.Assert(t, cmp.Contains(conflicts, "Conflicts: root-pkg-c <= 3.0.0"))

		// Test Provides - should contain root values
		provides := w.Provides().String()
		assert.Assert(t, cmp.Contains(provides, "Provides: common-pkg == 6.0.0"))
		assert.Assert(t, cmp.Contains(provides, "Provides: root-pkg-p == 5.0.0"))
	})
}
