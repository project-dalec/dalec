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
			// trim last newline from the first output since that has shifted
			s3 := s[:len(s)-1]
			if !strings.HasPrefix(s2, s3) {
				t.Fatalf("expected output to start with %q, got %q", s, out2.String())
			}

			s2 = strings.TrimPrefix(out2.String(), s3)
			expected := "Source1: " + pipDepsName + ".tar.gz\n\n"
			if s2 != expected {
				t.Fatalf("unexpected sources: expected %q, got: %q", expected, s2)
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

		// Now we should have entries for gomods, cargohome, and pip deps.
		// Note there are 2 gomod sources but they should be combined into one entry.

		expected := "Source7: " + gomodsName + ".tar.gz\nSource8: " + cargohomeName + ".tar.gz\nSource9: " + pipDepsName + ".tar.gz\n\n"
		if s != expected {
			t.Fatalf("generators: unexpected sources: expected %q, got: %q", expected, s)
		}
		s = s[len(expected):]
		if s != "" {
			t.Fatalf("unexpected trailing sources: %q", s)
		}
	})

	t.Run("source filter docs", func(t *testing.T) {
		w := &specWrapper{
			Spec: &dalec.Spec{
				Sources: map[string]dalec.Source{
					"src1": {
						Includes: []string{"cmd/**"},
						Excludes: []string{"testdata/**"},
						Inline: &dalec.SourceInline{
							Dir: &dalec.SourceInlineDir{},
						},
					},
				},
			},
			SourceFilter: dalec.SourceFilterConfig{GlobalExcludes: []string{"vendor/**"}},
		}

		out, err := w.Sources()
		assert.NilError(t, err)
		s := out.String()
		assert.Check(t, strings.Contains(s, "# \tIncludes:\n# \t\t cmd/**\n"))
		assert.Check(t, strings.Contains(s, "# \tExcludes:\n# \t\t testdata/**\n"))
		assert.Check(t, strings.Contains(s, "# Exclusions:\n# \tvendor/**\n"))
	})

	t.Run("source filter docs multiline exclusion", func(t *testing.T) {
		w := &specWrapper{
			Spec: &dalec.Spec{
				Sources: map[string]dalec.Source{
					"src1": {
						Inline: &dalec.SourceInline{Dir: &dalec.SourceInlineDir{}},
					},
				},
			},
			SourceFilter: dalec.SourceFilterConfig{GlobalExcludes: []string{"vendor/**\nmalicious: value"}},
		}

		out, err := w.Sources()
		assert.NilError(t, err)
		s := out.String()
		assert.Check(t, strings.Contains(s, "# Exclusions:\n# \tvendor/**\n# \tmalicious: value\n"))
		assert.Check(t, !strings.Contains(s, "\nmalicious: value\n"))
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
							Start:  true,
						},
					},
				},
			},
		}}

		assert.Equal(t, w.Post().String(),
			`%post
if [ $1 -eq 1 ]; then
    systemctl enable test2.service || :
fi
if [ $1 -eq 1 ]; then
    systemctl enable test3.service || :
fi
if [ $1 -eq 1 ] && [ -d /run/systemd/system ]; then
    systemctl start test3.service || :
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
							Start:  true,
						},
					},
				},
			},
		}}

		assert.Equal(t, w.PreUn().String(),
			`%preun
%systemd_preun test2.service
%systemd_preun test3.service
`)
	})

	t.Run("renamed systemd units are disabled under their installed name", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"src/original.service": {
							Name:   "installed.service",
							Enable: true,
						},
					},
				},
			},
		}}

		got := w.PreUn().String()
		assert.Equal(t, got, "%preun\n%systemd_preun installed.service\n")
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

	t.Run("the root package controls automatic requirement generation", func(t *testing.T) {
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

	t.Run("coreutils is required exactly when ownership commands are generated", func(t *testing.T) {
		cases := []struct {
			name      string
			artifacts dalec.Artifacts
			scripts   []string
		}{
			{
				name: "binary user ownership emits chown",
				artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{"app": {User: "svc"}},
				},
				scripts: []string{"chown -R svc /%{_bindir}/app\n"},
			},
			{
				name: "library group ownership emits chgrp",
				artifacts: dalec.Artifacts{
					Libs: map[string]dalec.ArtifactConfig{"libapp.so": {Group: "svc"}},
				},
				scripts: []string{"chgrp -R svc /%{_libdir}/libapp.so\n"},
			},
			{
				name: "libexec user ownership emits chown",
				artifacts: dalec.Artifacts{
					Libexec: map[string]dalec.ArtifactConfig{"helper": {User: "svc"}},
				},
				scripts: []string{"chown -R svc /%{_libexecdir}/helper\n"},
			},
			{
				name: "config file user ownership emits chown",
				artifacts: dalec.Artifacts{
					ConfigFiles: map[string]dalec.ArtifactConfig{"app.conf": {User: "svc"}},
				},
				scripts: []string{"chown -R svc /%{_sysconfdir}/app.conf\n"},
			},
			{
				name: "data file group ownership emits chgrp",
				artifacts: dalec.Artifacts{
					DataDirs: map[string]dalec.ArtifactConfig{"data": {Group: "svc"}},
				},
				scripts: []string{"chgrp -R svc /%{_datadir}/data\n"},
			},
			{
				name: "config directory user ownership emits chown",
				artifacts: dalec.Artifacts{
					Directories: &dalec.CreateArtifactDirectories{
						Config: map[string]dalec.ArtifactDirConfig{"app": {User: "svc"}},
					},
				},
				scripts: []string{"chown -R svc /%{_sysconfdir}/app\n"},
			},
			{
				name: "state directory group ownership emits chgrp",
				artifacts: dalec.Artifacts{
					Directories: &dalec.CreateArtifactDirectories{
						State: map[string]dalec.ArtifactDirConfig{"app": {Group: "svc"}},
					},
				},
				scripts: []string{"chgrp -R svc /%{_sharedstatedir}/app\n"},
			},
			{
				name: "declared symlink ownership emits chown and chgrp",
				artifacts: dalec.Artifacts{
					Users:  []dalec.AddUserConfig{{Name: "svc"}},
					Groups: []dalec.AddGroupConfig{{Name: "svc"}},
					Links: []dalec.ArtifactSymlinkConfig{{
						Source: "/usr/bin/app",
						Dest:   "/usr/bin/app-link",
						User:   "svc",
						Group:  "svc",
					}},
				},
				scripts: []string{
					"chown -h svc /usr/bin/app-link\n",
					"chgrp -h svc /usr/bin/app-link\n",
				},
			},
			{
				name: "empty file and directory ownership emits no command",
				artifacts: dalec.Artifacts{
					Binaries:    map[string]dalec.ArtifactConfig{"app": {}},
					Libexec:     map[string]dalec.ArtifactConfig{"helper": {}},
					Libs:        map[string]dalec.ArtifactConfig{"libapp.so": {}},
					ConfigFiles: map[string]dalec.ArtifactConfig{"app.conf": {}},
					DataDirs:    map[string]dalec.ArtifactConfig{"data": {}},
					Directories: &dalec.CreateArtifactDirectories{
						Config: map[string]dalec.ArtifactDirConfig{"config": {}},
						State:  map[string]dalec.ArtifactDirConfig{"state": {}},
					},
				},
			},
			{
				name: "undeclared symlink ownership emits no command",
				artifacts: dalec.Artifacts{
					Links: []dalec.ArtifactSymlinkConfig{{
						Source: "/usr/bin/app",
						Dest:   "/usr/bin/app-link",
						User:   "svc",
						Group:  "svc",
					}},
				},
			},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				w := specWrapper{Spec: &dalec.Spec{Artifacts: tc.artifacts}}

				ownershipScript := artifactOwnershipScript(tc.artifacts) +
					directoryOwnershipScript(tc.artifacts) +
					symlinkOwnershipScript(tc.artifacts)
				wantRequires := ""
				if ownershipScript != "" {
					wantRequires = "Requires(post): coreutils\n"
				}
				assert.Equal(t, getOwnershipPostRequires(tc.artifacts), wantRequires)
				assert.Equal(t, strings.Contains(w.Requires().String(), "Requires(post): coreutils\n"), wantRequires != "")

				gotPost := w.Post().String()
				for _, script := range tc.scripts {
					assert.Assert(t, cmp.Contains(ownershipScript, script))
					assert.Assert(t, cmp.Contains(gotPost, script))
				}
				if len(tc.scripts) == 0 {
					assert.Equal(t, ownershipScript, "")
				}
			})
		}
	})
}

func TestTemplate_DisableStrip(t *testing.T) {
	t.Run("the root package can disable stripping", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{DisableStrip: true},
		}}

		assert.Equal(t, w.DisableStrip(), `%global __strip /bin/true`)
	})

	t.Run("stripping remains enabled when the root package does not disable it", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{}}

		assert.Equal(t, w.DisableStrip(), "")
	})
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

// TestTemplate_ProvidesUsersAndGroups verifies that declared users/groups
// are self-Provided as `user(X)` / `group(X)` virtual capabilities. See
// the comment on (*specWrapper).Provides() for the rpm-version-related
// rationale.
func TestTemplate_ProvidesUsersAndGroups(t *testing.T) {
	t.Parallel()

	t.Run("users and groups produce matching Provides", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Artifacts: dalec.Artifacts{
				Users: []dalec.AddUserConfig{
					{Name: "svc"},
					{Name: "other"},
				},
				Groups: []dalec.AddGroupConfig{
					{Name: "svc"},
				},
			},
		}}

		got := w.Provides().String()
		assert.Assert(t, cmp.Contains(got, "Provides: user(svc)\n"))
		assert.Assert(t, cmp.Contains(got, "Provides: user(other)\n"))
		assert.Assert(t, cmp.Contains(got, "Provides: group(svc)\n"))
	})

	t.Run("no users or groups produces no user/group Provides", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{}}

		got := w.Provides().String()
		assert.Assert(t, !strings.Contains(got, "user("))
		assert.Assert(t, !strings.Contains(got, "group("))
	})

	t.Run("explicit Provides coexist with user/group Provides", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Provides: map[string]dalec.PackageConstraints{
				"some-cap": {Version: []string{"= 1.0"}},
			},
			Artifacts: dalec.Artifacts{
				Users:  []dalec.AddUserConfig{{Name: "svc"}},
				Groups: []dalec.AddGroupConfig{{Name: "svc"}},
			},
		}}

		got := w.Provides().String()
		assert.Assert(t, cmp.Contains(got, "Provides: some-cap == 1.0"))
		assert.Assert(t, cmp.Contains(got, "Provides: user(svc)\n"))
		assert.Assert(t, cmp.Contains(got, "Provides: group(svc)\n"))
	})
}

func TestTemplate_PackagesOwnOnlyDeclaredManpages(t *testing.T) {
	t.Parallel()

	w := &specWrapper{
		Spec: &dalec.Spec{
			Name: "foo",
			Artifacts: dalec.Artifacts{
				Manpages: map[string]dalec.ArtifactConfig{
					"docs/foo.1": {SubPath: "man1", Name: "primary.1"},
				},
			},
			Targets: map[string]dalec.Target{
				"azlinux3": {
					Packages: map[string]dalec.SubPackage{
						"docs": {
							Description: "Additional manual pages",
							Artifacts: &dalec.Artifacts{
								Manpages: map[string]dalec.ArtifactConfig{
									"docs/foo-admin.8": {SubPath: "man8"},
								},
							},
						},
					},
				},
			},
		},
		Target: "azlinux3",
	}

	primary := w.Files().String()
	supplemental, err := w.SubPackages()
	assert.NilError(t, err)

	assert.Assert(t, cmp.Contains(primary, "%{_mandir}/man1/primary.1\n"))
	assert.Assert(t, !strings.Contains(primary, "foo-admin.8"))
	assert.Assert(t, cmp.Contains(supplemental.String(), "%{_mandir}/man8/foo-admin.8\n"))
	assert.Assert(t, !strings.Contains(supplemental.String(), "primary.1"))
	assert.Assert(t, !strings.Contains(primary+supplemental.String(), "%{_mandir}/*/*"))
}

func TestTemplate_SupplementalPackagePreamble(t *testing.T) {
	t.Run("each supplemental package controls automatic requirement generation independently", func(t *testing.T) {
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"disabled": {
								Name:        "custom-disabled",
								Description: "Automatic requirements disabled",
								Artifacts: &dalec.Artifacts{
									DisableAutoRequires: true,
								},
							},
							"enabled": {
								Name:        "custom-enabled",
								Description: "Automatic requirements enabled",
								Artifacts:   &dalec.Artifacts{},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		out := got.String()

		assert.Assert(t, cmp.Contains(out, "%package -n custom-disabled\nSummary: Automatic requirements disabled\nAutoReq: no\n"))
		assert.Assert(t, cmp.Contains(out, "%package -n custom-enabled\nSummary: Automatic requirements enabled\n\n"))
		assert.Equal(t, strings.Count(out, "AutoReq: no"), 1)
	})

	t.Run("the root automatic-requirements setting is not inherited by a supplemental package without artifacts", func(t *testing.T) {
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Artifacts: dalec.Artifacts{
					DisableAutoRequires: true,
				},
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"meta": {
								Name:        "custom-meta",
								Description: "Metadata only",
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)

		assert.Equal(t, w.DisableAutoReq(), "AutoReq: no")
		assert.Assert(t, cmp.Contains(got.String(), "%package -n custom-meta\nSummary: Metadata only\n\n"))
		assert.Assert(t, !strings.Contains(got.String(), "AutoReq: no"))
	})

	t.Run("declared users and groups provide their RPM capabilities", func(t *testing.T) {
		got := renderSupplementalPackage(t, dalec.Artifacts{
			Users:  []dalec.AddUserConfig{{Name: "svcuser"}},
			Groups: []dalec.AddGroupConfig{{Name: "svcgroup"}},
			Links: []dalec.ArtifactSymlinkConfig{{
				Source: "/usr/bin/foo",
				Dest:   "/usr/bin/foo-link",
				User:   "svcuser",
				Group:  "svcgroup",
			}},
		})

		assert.Assert(t, cmp.Contains(got, "Provides: user(svcuser)\n"))
		assert.Assert(t, cmp.Contains(got, "Provides: group(svcgroup)\n"))
		assert.Assert(t, cmp.Contains(got, "%attr(-, svcuser, svcgroup) /usr/bin/foo-link\n"))
	})

	t.Run("ownership changes require coreutils", func(t *testing.T) {
		got := renderSupplementalPackage(t, dalec.Artifacts{
			ConfigFiles: map[string]dalec.ArtifactConfig{
				"foo.conf": {User: "svcuser"},
			},
		})

		assert.Assert(t, cmp.Contains(got, "Requires(post): coreutils\n"))
		assert.Assert(t, cmp.Contains(got, "chown -R svcuser /%{_sysconfdir}/foo.conf\n"))
	})

	t.Run("artifacts without ownership do not require coreutils", func(t *testing.T) {
		got := renderSupplementalPackage(t, dalec.Artifacts{
			Binaries: map[string]dalec.ArtifactConfig{"foo": {}},
		})

		assert.Assert(t, !strings.Contains(got, "Requires(post): coreutils"))
	})

	t.Run("renamed systemd units use their installed name in every scriptlet", func(t *testing.T) {
		got := renderSupplementalPackage(t, dalec.Artifacts{
			Systemd: &dalec.SystemdConfiguration{
				Units: map[string]dalec.SystemdUnitConfig{
					"src/original.service": {
						Name:   "installed.service",
						Enable: true,
					},
				},
			},
		})

		assert.Assert(t, cmp.Contains(got, "systemctl enable installed.service || :"))
		assert.Assert(t, cmp.Contains(got, "%systemd_preun installed.service\n"))
		assert.Assert(t, cmp.Contains(got, "%systemd_postun installed.service\n"))
		assert.Assert(t, !strings.Contains(got, "%systemd_preun original.service"))
	})
}

func TestTemplate_SubPackages(t *testing.T) {
	t.Run("no supplemental packages", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{Spec: &dalec.Spec{Name: "foo"}}
		got, err := w.SubPackages()
		assert.NilError(t, err)
		assert.Equal(t, got.String(), "")
	})

	t.Run("single subpackage with default name", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"debug": {
								Description: "Debug symbols for foo",
								Artifacts: &dalec.Artifacts{
									Binaries: map[string]dalec.ArtifactConfig{
										"foo-debug": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		// Should have %package with -n foo-debug (default naming: parent-key)
		assert.Assert(t, cmp.Contains(s, "%package -n foo-debug"))
		assert.Assert(t, cmp.Contains(s, "Summary: Debug symbols for foo"))
		assert.Assert(t, cmp.Contains(s, "%description -n foo-debug"))
		assert.Assert(t, cmp.Contains(s, "Debug symbols for foo"))
		assert.Assert(t, cmp.Contains(s, "%files -n foo-debug"))
		assert.Assert(t, cmp.Contains(s, "%{_bindir}/foo-debug"))
	})

	t.Run("subpackage with custom name", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"compat": {
								Name:        "foo-compat-v2",
								Description: "Backward compatibility shim",
								Artifacts: &dalec.Artifacts{
									Binaries: map[string]dalec.ArtifactConfig{
										"foo-v2": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		// Should use custom name
		assert.Assert(t, cmp.Contains(s, "%package -n foo-compat-v2"))
		assert.Assert(t, cmp.Contains(s, "Summary: Backward compatibility shim"))
		assert.Assert(t, cmp.Contains(s, "%description -n foo-compat-v2"))
		assert.Assert(t, cmp.Contains(s, "%files -n foo-compat-v2"))
		assert.Assert(t, cmp.Contains(s, "%{_bindir}/foo-v2"))
	})

	t.Run("subpackage with dependencies", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"devel": {
								Description: "Development files for foo",
								Dependencies: &dalec.SubPackageDependencies{
									Runtime: dalec.PackageDependencyList{
										"foo": dalec.PackageConstraints{
											Version: []string{"= %{version}-%{release}"},
										},
										"libfoo-headers": {},
									},
									Recommends: dalec.PackageDependencyList{
										"foo-docs": {},
									},
								},
								Provides: dalec.PackageDependencyList{
									"foo-dev": {},
								},
								Conflicts: dalec.PackageDependencyList{
									"foo-devel-old": {
										Version: []string{"< 1.0"},
									},
								},
								Replaces: dalec.PackageDependencyList{
									"foo-devel-legacy": {},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		// Dependencies
		assert.Assert(t, cmp.Contains(s, "Requires: foo == %{version}-%{release}"))
		assert.Assert(t, cmp.Contains(s, "Requires: libfoo-headers"))
		assert.Assert(t, cmp.Contains(s, "Recommends: foo-docs"))
		assert.Assert(t, cmp.Contains(s, "Provides: foo-dev"))
		assert.Assert(t, cmp.Contains(s, "Conflicts: foo-devel-old < 1.0"))
		assert.Assert(t, cmp.Contains(s, "Obsoletes: foo-devel-legacy"))
	})

	t.Run("multiple subpackages sorted", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"debug": {
								Description: "Debug package",
								Artifacts: &dalec.Artifacts{
									Binaries: map[string]dalec.ArtifactConfig{
										"foo-debug": {},
									},
								},
							},
							"contrib": {
								Description: "Contrib package",
								Artifacts: &dalec.Artifacts{
									Binaries: map[string]dalec.ArtifactConfig{
										"foo-contrib": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		// Both subpackages should be present
		assert.Assert(t, cmp.Contains(s, "%package -n foo-contrib"))
		assert.Assert(t, cmp.Contains(s, "%package -n foo-debug"))

		// contrib should come before debug (sorted by key)
		contribIdx := strings.Index(s, "%package -n foo-contrib")
		debugIdx := strings.Index(s, "%package -n foo-debug")
		assert.Assert(t, contribIdx < debugIdx, "contrib should appear before debug (sorted)")
	})

	t.Run("subpackage with nil artifacts", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"meta": {
								Description: "Meta package with no artifacts",
								Dependencies: &dalec.SubPackageDependencies{
									Runtime: dalec.PackageDependencyList{
										"foo":       {},
										"foo-debug": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		assert.Assert(t, cmp.Contains(s, "%package -n foo-meta"))
		assert.Assert(t, cmp.Contains(s, "%files -n foo-meta"))
		// Should have requires but empty files section
		assert.Assert(t, cmp.Contains(s, "Requires: foo\n"))
		assert.Assert(t, cmp.Contains(s, "Requires: foo-debug\n"))
	})

	t.Run("subpackage with systemd units", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"service": {
								Description: "Service package",
								Artifacts: &dalec.Artifacts{
									Systemd: &dalec.SystemdConfiguration{
										Units: map[string]dalec.SystemdUnitConfig{
											"foo-svc.service": {
												Enable: true,
												Start:  true,
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		// Should have service in %files
		assert.Assert(t, cmp.Contains(s, "%{_unitdir}/foo-svc.service"))
		// Should have %post for enabling
		assert.Assert(t, cmp.Contains(s, "%post -n foo-service"))
		assert.Assert(t, cmp.Contains(s, "systemctl enable foo-svc.service || :"))
		assert.Assert(t, cmp.Contains(s, "systemctl start foo-svc.service || :"))
		// Should have %preun for disabling
		assert.Assert(t, cmp.Contains(s, "%preun -n foo-service"))
		assert.Assert(t, cmp.Contains(s, "%systemd_preun foo-svc.service"))
		// Should have %postun
		assert.Assert(t, cmp.Contains(s, "%postun -n foo-service"))
		assert.Assert(t, cmp.Contains(s, "%systemd_postun foo-svc.service"))
		// Should declare install-time scriptlet requirements so the subpackage
		// pulls in systemd independently of the primary package.
		assert.Assert(t, cmp.Contains(s, "Requires(post): systemd\n"))
		assert.Assert(t, cmp.Contains(s, "Requires(preun): systemd\n"))
		assert.Assert(t, cmp.Contains(s, "Requires(postun): systemd\n"))
	})

	t.Run("subpackage with users requires adduser at install time", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"service": {
								Description: "Service package",
								Artifacts: &dalec.Artifacts{
									Users:  []dalec.AddUserConfig{{Name: "foouser"}},
									Groups: []dalec.AddGroupConfig{{Name: "foogroup"}},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		assert.Assert(t, cmp.Contains(s, "Requires(post): /usr/sbin/adduser, /usr/bin/getent\n"))
		assert.Assert(t, cmp.Contains(s, "Requires(post): /usr/sbin/groupadd, /usr/bin/getent\n"))
	})

	t.Run("subpackage docs and licenses use resolved name", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"devel": {
								Description: "Development files",
								Artifacts: &dalec.Artifacts{
									Docs: map[string]dalec.ArtifactConfig{
										"API.md": {},
									},
									Licenses: map[string]dalec.ArtifactConfig{
										"LICENSE": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		s := got.String()

		// Docs and licenses should use the subpackage's resolved name
		assert.Assert(t, cmp.Contains(s, "%doc %{_docdir}/foo-devel/API.md"))
		assert.Assert(t, cmp.Contains(s, "%license %{_licensedir}/foo-devel/LICENSE"))
	})

	t.Run("wrong target returns no subpackages", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"debug": {
								Description: "Debug",
							},
						},
					},
				},
			},
			Target: "jammy",
		}

		got, err := w.SubPackages()
		assert.NilError(t, err)
		assert.Equal(t, got.String(), "")
	})
}

func TestTemplate_SubPackageInstall(t *testing.T) {
	t.Run("install includes subpackage artifacts", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"foo": {},
					},
				},
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"debug": {
								Description: "Debug symbols",
								Artifacts: &dalec.Artifacts{
									Binaries: map[string]dalec.ArtifactConfig{
										"foo-debug": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got := w.Install().String()

		// Primary package artifact
		assert.Assert(t, cmp.Contains(got, "cp -r foo %{buildroot}/%{_bindir}/foo"))
		// Subpackage artifact
		assert.Assert(t, cmp.Contains(got, "cp -r foo-debug %{buildroot}/%{_bindir}/foo-debug"))
	})

	t.Run("install subpackage docs use resolved name", func(t *testing.T) {
		t.Parallel()
		w := &specWrapper{
			Spec: &dalec.Spec{
				Name: "foo",
				Targets: map[string]dalec.Target{
					"azlinux3": {
						Packages: map[string]dalec.SubPackage{
							"devel": {
								Description: "Dev files",
								Artifacts: &dalec.Artifacts{
									Docs: map[string]dalec.ArtifactConfig{
										"API.md": {},
									},
									Licenses: map[string]dalec.ArtifactConfig{
										"LICENSE": {},
									},
								},
							},
						},
					},
				},
			},
			Target: "azlinux3",
		}

		got := w.Install().String()

		// Docs and licenses should be installed under the subpackage's resolved name
		assert.Assert(t, cmp.Contains(got, "%{buildroot}/%{_docdir}/foo-devel"))
		assert.Assert(t, cmp.Contains(got, "%{buildroot}/%{_licensedir}/foo-devel"))
	})
}

func TestTemplate_SubPackageFullSpec(t *testing.T) {
	t.Parallel()

	spec := &dalec.Spec{
		Name:        "myapp",
		Version:     "1.0.0",
		Revision:    "1",
		Description: "My application",
		License:     "MIT",
		Targets: map[string]dalec.Target{
			"azlinux3": {
				Packages: map[string]dalec.SubPackage{
					"devel": {
						Description: "Development headers for myapp",
						Artifacts: &dalec.Artifacts{
							Headers: map[string]dalec.ArtifactConfig{
								"myapp.h": {},
							},
						},
						Dependencies: &dalec.SubPackageDependencies{
							Runtime: dalec.PackageDependencyList{
								"myapp": {
									Version: []string{"= %{version}-%{release}"},
								},
							},
						},
					},
				},
			},
		},
	}

	w := &strings.Builder{}
	err := WriteSpec(spec, "azlinux3", w)
	assert.NilError(t, err)

	s := w.String()

	// Primary package should be present
	assert.Assert(t, cmp.Contains(s, "Name: myapp"))
	assert.Assert(t, cmp.Contains(s, "Summary: My application"))

	// Subpackage should be present
	assert.Assert(t, cmp.Contains(s, "%package -n myapp-devel"))
	assert.Assert(t, cmp.Contains(s, "Summary: Development headers for myapp"))
	assert.Assert(t, cmp.Contains(s, "Requires: myapp == %{version}-%{release}"))
	assert.Assert(t, cmp.Contains(s, "%description -n myapp-devel"))
	assert.Assert(t, cmp.Contains(s, "Development headers for myapp"))
	assert.Assert(t, cmp.Contains(s, "%files -n myapp-devel"))
	assert.Assert(t, cmp.Contains(s, "%{_includedir}/myapp.h"))

	// Install should include subpackage headers
	assert.Assert(t, cmp.Contains(s, "cp -r myapp.h %{buildroot}/%{_includedir}/myapp.h"))
}

func renderSupplementalPackage(t *testing.T, artifacts dalec.Artifacts) string {
	t.Helper()

	w := &specWrapper{
		Spec: &dalec.Spec{
			Name: "foo",
			Targets: map[string]dalec.Target{
				"azlinux3": {
					Packages: map[string]dalec.SubPackage{
						"supplemental": {
							Description: "Supplemental package",
							Artifacts:   &artifacts,
						},
					},
				},
			},
		},
		Target: "azlinux3",
	}

	got, err := w.SubPackages()
	assert.NilError(t, err)
	return got.String()
}
