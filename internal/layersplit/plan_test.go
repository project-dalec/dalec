package layersplit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func writeTree(t *testing.T, root string, files map[string]string, links map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		assert.NilError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		assert.NilError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	for p, target := range links {
		full := filepath.Join(root, filepath.FromSlash(p))
		assert.NilError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		assert.NilError(t, os.Symlink(target, full))
	}
}

func testRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"/usr/lib/libc.so.6":        "libc",
		"/usr/lib/libssl.so.3":      "libssl",
		"/usr/bin/openssl":          "openssl",
		"/usr/bin/myapp":            "myapp",
		"/usr/bin/myapp-debug":      "myapp-debug",
		"/etc/passwd":               "root:x:0:0",
		"/etc/group":                "root:x:0:",
		"/etc/ld.so.cache":          "cache",
		"/etc/unowned.conf":         "x",
		"/var/lib/rpm/rpmdb.sqlite": "db",
		"/usr/share/doc/glibc/NEWS": "news",
	}, map[string]string{
		"/lib": "usr/lib",
	})
	return root
}

func testInputs() Inputs {
	return Inputs{
		Packages: map[string]string{
			"glibc":        "glibc",
			"openssl":      "openssl",
			"openssl-libs": "openssl",
			"setup":        "setup",
			"myapp":        "myapp",
			"myapp-debug":  "myapp",
		},
		Files: []OwnedFile{
			// Owned through the /lib -> usr/lib symlink.
			{Package: "glibc", Path: "/lib/libc.so.6"},
			{Package: "glibc", Path: "/etc/ld.so.cache", Ghost: true},
			{Package: "glibc", Path: "/usr/share/doc/glibc/NEWS"},
			{Package: "glibc", Path: "/usr/share/doc/glibc"},
			{Package: "openssl-libs", Path: "/usr/lib/libssl.so.3"},
			{Package: "openssl", Path: "/usr/bin/openssl"},
			{Package: "setup", Path: "/etc/passwd"},
			{Package: "setup", Path: "/etc/group"},
			{Package: "myapp", Path: "/usr/bin/myapp"},
			{Package: "myapp-debug", Path: "/usr/bin/myapp-debug"},
		},
		Modified: []string{"/etc/passwd"},
		App:      map[string]bool{"myapp": true, "myapp-debug": true},
	}
}

func groupNames(p *Plan) []string {
	var out []string
	for _, g := range p.Groups {
		out = append(out, g.Kind+":"+g.Name)
	}
	return out
}

func groupOf(t *testing.T, p *Plan, path string) string {
	t.Helper()
	idx, ok := p.Assign[path]
	assert.Assert(t, ok, "path %s not assigned", path)
	g := p.Groups[idx]
	return g.Kind + ":" + g.Name
}

func TestBuildPlan(t *testing.T) {
	root := testRoot(t)
	plan, err := BuildPlan(root, testInputs(), Options{})
	assert.NilError(t, err)

	assert.Check(t, cmp.DeepEqual(groupNames(plan), []string{
		"os:glibc", "os:openssl", "os:setup",
		"app:myapp", "app:myapp-debug",
		"state:state",
	}))

	assert.Check(t, cmp.DeepEqual(plan.Groups[1].Packages, []string{"openssl", "openssl-libs"}))

	cases := map[string]string{
		"/usr/lib/libc.so.6":        "os:glibc",
		"/usr/lib/libssl.so.3":      "os:openssl",
		"/usr/bin/openssl":          "os:openssl",
		"/usr/bin/myapp":            "app:myapp",
		"/usr/bin/myapp-debug":      "app:myapp-debug",
		"/usr/share/doc/glibc":      "os:glibc",
		"/usr/share/doc/glibc/NEWS": "os:glibc",
		"/etc/group":                "os:setup",
		// Modified after install.
		"/etc/passwd": "state:state",
		// %ghost file.
		"/etc/ld.so.cache": "state:state",
		// Not owned by any package.
		"/etc/unowned.conf": "state:state",
		"/lib":              "state:state",
		"/usr":              "state:state",
		// Package database.
		"/var/lib/rpm/rpmdb.sqlite": "state:state",
	}
	for p, want := range cases {
		assert.Check(t, cmp.Equal(groupOf(t, plan, p), want), p)
	}
}

func TestBuildPlanBudget(t *testing.T) {
	root := testRoot(t)
	plan, err := BuildPlan(root, testInputs(), Options{MaxLayers: 5})
	assert.NilError(t, err)

	// 5 layers: 2 app + state leaves 2 for OS packages: the largest one
	// keeps its own layer and the rest are folded into misc.
	assert.Check(t, cmp.DeepEqual(groupNames(plan), []string{
		"os:misc", "os:openssl", "app:myapp", "app:myapp-debug", "state:state",
	}))
	assert.Check(t, cmp.DeepEqual(plan.Groups[0].Packages, []string{"glibc", "setup"}))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/lib/libc.so.6"), "os:misc"))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/openssl"), "os:openssl"))

	_, err = BuildPlan(root, testInputs(), Options{MaxLayers: 3})
	assert.ErrorContains(t, err, "too small")
}

func TestBuildPlanDeterministic(t *testing.T) {
	root := testRoot(t)
	want, err := BuildPlan(root, testInputs(), Options{})
	assert.NilError(t, err)
	for range 10 {
		got, err := BuildPlan(root, testInputs(), Options{})
		assert.NilError(t, err)
		assert.DeepEqual(t, got.Groups, want.Groups)
		assert.DeepEqual(t, got.Assign, want.Assign)
	}
}

func TestResolveInRoot(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"/usr/lib/x": ""}, map[string]string{
		"/lib":      "usr/lib",
		"/abs":      "/usr/lib",
		"/loop/a":   "../loop/b",
		"/loop/b":   "a",
		"/usr/link": "../lib",
	})

	cases := map[string]string{
		"/lib/x":         "/usr/lib/x",
		"/abs/x":         "/usr/lib/x",
		"/usr/link/x":    "/usr/lib/x",
		"/lib":           "/lib",
		"/missing/a/b":   "/missing/a/b",
		"/lib/missing/b": "/usr/lib/missing/b",
		"/usr/../lib/x":  "/usr/lib/x",
	}
	for in, want := range cases {
		got, err := ResolveInRoot(root, in)
		assert.NilError(t, err, in)
		assert.Check(t, cmp.Equal(got, want), in)
	}

	_, err := ResolveInRoot(root, "/loop/a/x")
	assert.ErrorContains(t, err, "too many levels")
}

func TestHistoryComment(t *testing.T) {
	g := Group{Name: "openssl", Kind: KindOS, Packages: []string{"openssl", "openssl-libs"}}
	c := g.HistoryComment()
	assert.Check(t, cmp.Equal(c, "dalec.layer.v1 kind=os name=openssl packages=openssl,openssl-libs"))

	parsed, ok := ParseHistoryComment(c)
	assert.Assert(t, ok)
	assert.Check(t, cmp.DeepEqual(parsed, Group{Name: g.Name, Kind: g.Kind, Packages: g.Packages}))

	parsed, ok = ParseHistoryComment(Group{Name: KindState, Kind: KindState}.HistoryComment())
	assert.Assert(t, ok)
	assert.Check(t, cmp.Equal(parsed.Kind, KindState))
	assert.Check(t, cmp.Len(parsed.Packages, 0))

	_, ok = ParseHistoryComment("buildkit.exporter.image.v0")
	assert.Check(t, !ok)
}

func TestParsers(t *testing.T) {
	t.Run("source package name", func(t *testing.T) {
		assert.Check(t, cmp.Equal(SourcePackageName("openssl-3.3.2-1.azl3.src.rpm"), "openssl"))
		assert.Check(t, cmp.Equal(SourcePackageName("ca-certificates-3.0.0-8.azl3.src.rpm"), "ca-certificates"))
		assert.Check(t, cmp.Equal(SourcePackageName("(none)"), ""))
	})

	t.Run("packages", func(t *testing.T) {
		pkgs, err := ParseRPMPackages(strings.NewReader("openssl-libs\topenssl-3.3.2-1.azl3.src.rpm\ngpg-pubkey\t(none)\n"))
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(pkgs, map[string]string{"openssl-libs": "openssl", "gpg-pubkey": "gpg-pubkey"}))
	})

	t.Run("files", func(t *testing.T) {
		in := "glibc\tnormal\t\t/usr/lib/libc.so.6\n" +
			"glibc\tnormal\tg\t/etc/ld.so.cache\n" +
			"glibc\tnot installed\td\t/usr/share/doc/glibc/NEWS\n" +
			"setup\treplaced\tc\t/etc/passwd\n"
		files, err := ParseRPMFiles(strings.NewReader(in))
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(files, []OwnedFile{
			{Package: "glibc", Path: "/usr/lib/libc.so.6"},
			{Package: "glibc", Path: "/etc/ld.so.cache", Ghost: true},
			{Package: "setup", Path: "/etc/passwd"},
		}))

		_, err = ParseRPMFiles(strings.NewReader("bad line\n"))
		assert.ErrorContains(t, err, "malformed")
	})

	t.Run("verify", func(t *testing.T) {
		in := "S.5....T.  c /etc/passwd\n" +
			"missing   c /etc/removed\n" +
			".M.......    /usr/bin/with space\n"
		paths, err := ParseRPMVerify(strings.NewReader(in))
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(paths, []string{"/etc/passwd", "/usr/bin/with space"}))
	})

	t.Run("assign roundtrip", func(t *testing.T) {
		want := map[string]int{"/a": 0, "/b c": 2, "/d\ne": 1}
		var buf bytes.Buffer
		assert.NilError(t, WriteAssign(&buf, want))
		got, err := ReadAssign(&buf)
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(got, want))

		got, err = ReadAssign(strings.NewReader(""))
		assert.NilError(t, err)
		assert.Check(t, cmp.Len(got, 0))
	})

	t.Run("plan roundtrip", func(t *testing.T) {
		p := &Plan{Version: PlanVersion, Groups: []Group{{Name: "glibc", Kind: KindOS}, {Name: KindState, Kind: KindState}}}
		dt, err := MarshalPlan(p)
		assert.NilError(t, err)
		got, err := UnmarshalPlan(dt)
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(got.Groups, p.Groups))

		_, err = UnmarshalPlan([]byte(`{"version":1,"groups":[{"name":"glibc","kind":"os"}]}`))
		assert.ErrorContains(t, err, "state layer")
		_, err = UnmarshalPlan([]byte(`{"version":2}`))
		assert.ErrorContains(t, err, "version")
	})
}

func TestBuildPlanKeepsHardlinksTogether(t *testing.T) {
	root := testRoot(t)
	// One file with paths owned by two different packages.
	assert.NilError(t, os.Link(filepath.Join(root, "usr/bin/openssl"), filepath.Join(root, "usr/bin/myapp-openssl")))
	// A hardlink set owned by a single package stays in its layer.
	assert.NilError(t, os.Link(filepath.Join(root, "usr/bin/myapp"), filepath.Join(root, "usr/bin/myapp-alias")))

	in := testInputs()
	in.Files = append(in.Files,
		OwnedFile{Package: "myapp", Path: "/usr/bin/myapp-openssl"},
		OwnedFile{Package: "myapp", Path: "/usr/bin/myapp-alias"},
	)
	plan, err := BuildPlan(root, in, Options{})
	assert.NilError(t, err)

	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/openssl"), "state:state"))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp-openssl"), "state:state"))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp"), "app:myapp"))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp-alias"), "app:myapp"))

	// Group counters follow the moved paths.
	total := 0
	for _, g := range plan.Groups {
		total += g.Paths
	}
	assert.Check(t, cmp.Equal(total, len(plan.Assign)))
}

func TestBuildPlanSpecPackages(t *testing.T) {
	root := testRoot(t)
	in := testInputs()
	// An installed spec metapackage without files, and a spec package that is
	// not installed.
	in.Packages["myapp-meta"] = "myapp"
	in.App["myapp-meta"] = true
	in.App["myapp-missing"] = true

	plan, err := BuildPlan(root, in, Options{})
	assert.NilError(t, err)
	assert.Check(t, cmp.DeepEqual(plan.SpecPackages, []string{"myapp", "myapp-debug", "myapp-meta"}))
	for _, g := range plan.Groups {
		assert.Check(t, g.Name != "myapp-meta", "a package without files has no layer")
	}
}
