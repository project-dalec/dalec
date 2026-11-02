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
		"/usr/bin/bash":             "bash",
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

// testInputs describes a root filesystem with the base package "dalec-base"
// (depending on setup and glibc), the spec packages myapp and myapp-debug,
// and their runtime dependencies openssl, openssl-libs and bash.
func testInputs() Inputs {
	return Inputs{
		Packages: map[string]bool{
			"dalec-base":   true,
			"glibc":        true,
			"setup":        true,
			"bash":         true,
			"openssl":      true,
			"openssl-libs": true,
			"myapp":        true,
			"myapp-debug":  true,
		},
		Deps: Deps{
			Requires: map[string][][]string{
				"dalec-base":   {{"setup"}, {"libc.so.6()(64bit)"}},
				"glibc":        {{"setup"}},
				"openssl":      {{"openssl-libs"}, {"/bin/sh"}},
				"openssl-libs": {{"libc.so.6()(64bit)"}},
				"bash":         {{"glibc"}},
				"myapp":        {{"openssl"}},
				"myapp-debug":  {{"myapp"}},
			},
			Provides: map[string][]string{
				"libc.so.6()(64bit)": {"glibc"},
			},
		},
		Files: []OwnedFile{
			// Owned through the /lib -> usr/lib symlink.
			{Package: "glibc", Path: "/lib/libc.so.6"},
			{Package: "glibc", Path: "/etc/ld.so.cache", Ghost: true},
			{Package: "glibc", Path: "/usr/share/doc/glibc/NEWS"},
			{Package: "glibc", Path: "/usr/share/doc/glibc"},
			{Package: "openssl-libs", Path: "/usr/lib/libssl.so.3"},
			{Package: "openssl", Path: "/usr/bin/openssl"},
			{Package: "bash", Path: "/usr/bin/bash"},
			{Package: "bash", Path: "/bin/sh"},
			{Package: "setup", Path: "/etc/passwd"},
			{Package: "setup", Path: "/etc/group"},
			{Package: "myapp", Path: "/usr/bin/myapp"},
			{Package: "myapp-debug", Path: "/usr/bin/myapp-debug"},
		},
		Modified: []string{"/etc/passwd"},
		App:      map[string]bool{"myapp": true, "myapp-debug": true},
		Base:     map[string]bool{"dalec-base": true},
	}
}

func groupKinds(p *Plan) []string {
	var out []string
	for _, g := range p.Groups {
		out = append(out, g.Kind)
	}
	return out
}

func groupOf(t *testing.T, p *Plan, path string) string {
	t.Helper()
	idx, ok := p.Assign[path]
	assert.Assert(t, ok, "path %s not assigned", path)
	return p.Groups[idx].Kind
}

func TestBuildPlan(t *testing.T) {
	root := testRoot(t)
	plan, err := BuildPlan(root, testInputs())
	assert.NilError(t, err)

	assert.Check(t, cmp.DeepEqual(groupKinds(plan), []string{KindBase, KindDeps, KindApp, KindState}))
	assert.Check(t, cmp.DeepEqual(plan.Groups[0].Packages, []string{"dalec-base", "glibc", "setup"}))
	// bash is only reached through a file dependency of openssl, which is
	// not a base package.
	assert.Check(t, cmp.DeepEqual(plan.Groups[1].Packages, []string{"bash", "openssl", "openssl-libs"}))
	assert.Check(t, cmp.DeepEqual(plan.Groups[2].Packages, []string{"myapp", "myapp-debug"}))

	cases := map[string]string{
		"/usr/lib/libc.so.6":        KindBase,
		"/usr/share/doc/glibc":      KindBase,
		"/usr/share/doc/glibc/NEWS": KindBase,
		"/etc/group":                KindBase,
		"/usr/lib/libssl.so.3":      KindDeps,
		"/usr/bin/openssl":          KindDeps,
		"/usr/bin/bash":             KindDeps,
		"/usr/bin/myapp":            KindApp,
		"/usr/bin/myapp-debug":      KindApp,
		// Modified after install.
		"/etc/passwd": KindState,
		// %ghost file.
		"/etc/ld.so.cache": KindState,
		// Not owned by any package.
		"/etc/unowned.conf": KindState,
		"/lib":              KindState,
		"/usr":              KindState,
		// Package database.
		"/var/lib/rpm/rpmdb.sqlite": KindState,
	}
	for p, want := range cases {
		assert.Check(t, cmp.Equal(groupOf(t, plan, p), want), p)
	}
}

func TestBuildPlanOmitsEmptySets(t *testing.T) {
	root := testRoot(t)
	in := testInputs()
	in.Base = nil
	in.App = nil

	plan, err := BuildPlan(root, in)
	assert.NilError(t, err)
	assert.Check(t, cmp.DeepEqual(groupKinds(plan), []string{KindDeps, KindState}))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp"), KindDeps))
}

func TestBuildPlanSharedPathGoesToEarliestLayer(t *testing.T) {
	root := testRoot(t)
	in := testInputs()
	in.Files = append(in.Files,
		OwnedFile{Package: "myapp", Path: "/usr/share/doc/glibc"},
		OwnedFile{Package: "openssl", Path: "/usr/bin/myapp"},
	)

	plan, err := BuildPlan(root, in)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/share/doc/glibc"), KindBase))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp"), KindDeps))
}

func TestClassify(t *testing.T) {
	t.Run("file dependencies", func(t *testing.T) {
		in := testInputs()
		in.Base = map[string]bool{"glibc": true}
		in.Deps.Requires["glibc"] = append(in.Deps.Requires["glibc"], []string{"/bin/sh"})
		owners := func(p string) []string {
			if p == "/bin/sh" {
				return []string{"bash"}
			}
			return nil
		}
		got := Classify(in, owners)
		assert.Check(t, cmp.Equal(got["bash"], KindBase))
		assert.Check(t, cmp.Equal(got["dalec-base"], KindDeps))
	})

	t.Run("alternatives", func(t *testing.T) {
		in := Inputs{
			Packages: map[string]bool{"base": true, "mawk": true, "gawk": true, "dash": true},
			Deps: Deps{
				Requires: map[string][][]string{"base": {{"awk", "missing"}}},
				Provides: map[string][]string{"awk": {"mawk", "gawk"}},
			},
			Base: map[string]bool{"base": true},
		}
		got := Classify(in, nil)
		assert.Check(t, cmp.DeepEqual(got, map[string]string{
			"base": KindBase, "mawk": KindBase, "gawk": KindBase, "dash": KindDeps,
		}))
	})

	t.Run("spec packages are never base", func(t *testing.T) {
		in := Inputs{
			Packages: map[string]bool{"base": true, "app": true, "lib": true},
			Deps:     Deps{Requires: map[string][][]string{"base": {{"app"}}, "app": {{"lib"}}}},
			App:      map[string]bool{"app": true},
			Base:     map[string]bool{"base": true, "not-installed": true},
		}
		got := Classify(in, nil)
		assert.Check(t, cmp.DeepEqual(got, map[string]string{"base": KindBase, "app": KindApp, "lib": KindDeps}))
	})

	t.Run("dependency cycles", func(t *testing.T) {
		in := Inputs{
			Packages: map[string]bool{"a": true, "b": true},
			Deps:     Deps{Requires: map[string][][]string{"a": {{"b"}}, "b": {{"a"}}}},
			Base:     map[string]bool{"a": true},
		}
		got := Classify(in, nil)
		assert.Check(t, cmp.DeepEqual(got, map[string]string{"a": KindBase, "b": KindBase}))
	})
}

func TestBuildPlanDeterministic(t *testing.T) {
	root := testRoot(t)
	want, err := BuildPlan(root, testInputs())
	assert.NilError(t, err)
	for range 10 {
		got, err := BuildPlan(root, testInputs())
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
		"/alias":    "usr/bin",
		"/rel":      "alias/../share",
	})

	cases := map[string]string{
		"/lib/x":         "/usr/lib/x",
		"/abs/x":         "/usr/lib/x",
		"/usr/link/x":    "/usr/lib/x",
		"/lib":           "/lib",
		"/missing/a/b":   "/missing/a/b",
		"/lib/missing/b": "/usr/lib/missing/b",
		"/usr/../lib/x":  "/usr/lib/x",
		// ".." applies to the resolved directory: /alias is /usr/bin.
		"/alias/../victim": "/usr/victim",
		"/rel/x":           "/usr/share/x",
		"/../../x/../..":   "/",
		"/./usr/./lib/x":   "/usr/lib/x",
		// Symlinks reached after a missing directory are still resolved.
		"/missing/../lib/x":      "/usr/lib/x",
		"/missing/a/../../abs/x": "/usr/lib/x",
	}
	for in, want := range cases {
		got, err := ResolveInRoot(root, in)
		assert.NilError(t, err, in)
		assert.Check(t, cmp.Equal(got, want), in)
	}

	_, err := ResolveInRoot(root, "/loop/a/x")
	assert.ErrorContains(t, err, "too many levels")
}

func TestHistoryCreatedBy(t *testing.T) {
	cases := []struct {
		g    Group
		want string
	}{
		{Group{Kind: KindBase, Packages: []string{"glibc"}}, "dalec: base packages: glibc"},
		{Group{Kind: KindDeps, Packages: []string{"openssl", "openssl-libs"}}, "dalec: runtime dependencies: openssl, openssl-libs"},
		{Group{Kind: KindApp, Packages: []string{"myapp"}}, "dalec: packages: myapp"},
		{Group{Kind: KindState}, "dalec: package database and generated files"},
	}
	for _, c := range cases {
		assert.Check(t, cmp.Equal(c.g.HistoryCreatedBy(), c.want))
	}
}

func TestParsers(t *testing.T) {
	t.Run("rpm requires and provides", func(t *testing.T) {
		var d Deps
		requires := "bash\tglibc\n" +
			"bash\t/bin/sh\n" +
			"bash\trpmlib(CompressedFileNames)\n" +
			"bash\tlibc.so.6(GLIBC_2.34)(64bit)\n" +
			"app\t(foo >= 1.0 or (libc.so.6(GLIBC_2.34)(64bit) if bar))\n" +
			"empty\t\n"
		assert.NilError(t, ParseRPMRequires(strings.NewReader(requires), &d))
		assert.Check(t, cmp.DeepEqual(d.Requires, map[string][][]string{
			"bash": {{"glibc"}, {"/bin/sh"}, {"libc.so.6(GLIBC_2.34)(64bit)"}},
			"app":  {{"foo", "libc.so.6(GLIBC_2.34)(64bit)", "bar"}},
		}))

		provides := "glibc\tglibc\nglibc\tlibc.so.6(GLIBC_2.34)(64bit)\nglibc\tglibc\n"
		assert.NilError(t, ParseRPMProvides(strings.NewReader(provides), &d))
		assert.Check(t, cmp.DeepEqual(d.Provides, map[string][]string{
			"glibc":                        {"glibc"},
			"libc.so.6(GLIBC_2.34)(64bit)": {"glibc"},
		}))
	})

	t.Run("dpkg dependencies", func(t *testing.T) {
		var d Deps
		in := "base-files\tawk\t\t\t\n" +
			"bash\t\tbase-files (>= 2.1.12), debianutils (>= 5.6-0.1)\tbash-completion\t\n" +
			"mawk\t\tlibc6 (>= 2.34)\t\tawk\n" +
			"libssl3t64\t\tlibc6:amd64 (>= 2.38) | libc6-alt, zlib1g [amd64]\t\tlibssl3 (= 3.0.13), libssl\n"
		assert.NilError(t, ParseDpkgDeps(strings.NewReader(in), &d))
		assert.Check(t, cmp.DeepEqual(d.Requires, map[string][][]string{
			"base-files": {{"awk"}},
			"bash":       {{"base-files"}, {"debianutils"}, {"bash-completion"}},
			"mawk":       {{"libc6"}},
			"libssl3t64": {{"libc6", "libc6-alt"}, {"zlib1g"}},
		}))
		assert.Check(t, cmp.DeepEqual(d.Provides, map[string][]string{
			"awk":     {"mawk"},
			"libssl3": {"libssl3t64"},
			"libssl":  {"libssl3t64"},
		}))

		assert.ErrorContains(t, ParseDpkgDeps(strings.NewReader("bad line\n"), &d), "malformed")
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
			".M.......    /usr/bin/with space\n" +
			// Lines added for dpkg diversions and stat overrides.
			"diverted /usr/bin/foo.distrib\n" +
			"statoverride /usr/bin/ping\n"
		paths, err := ParseRPMVerify(strings.NewReader(in))
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(paths, []string{"/etc/passwd", "/usr/bin/with space", "/usr/bin/foo.distrib", "/usr/bin/ping"}))
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
		p := &Plan{Version: PlanVersion, Groups: []Group{{Kind: KindBase, Packages: []string{"glibc"}}, {Kind: KindState}}}
		dt, err := MarshalPlan(p)
		assert.NilError(t, err)
		got, err := UnmarshalPlan(dt)
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(got.Groups, p.Groups))

		_, err = UnmarshalPlan([]byte(`{"version":2,"groups":[{"kind":"base"}]}`))
		assert.ErrorContains(t, err, "state layer")
		_, err = UnmarshalPlan([]byte(`{"version":1}`))
		assert.ErrorContains(t, err, "version")
	})
}

func TestBuildPlanKeepsHardlinksTogether(t *testing.T) {
	root := testRoot(t)
	// One file with paths owned by packages of two different sets.
	assert.NilError(t, os.Link(filepath.Join(root, "usr/bin/openssl"), filepath.Join(root, "usr/bin/myapp-openssl")))
	// A hardlink set owned by a single package stays in its layer.
	assert.NilError(t, os.Link(filepath.Join(root, "usr/bin/myapp"), filepath.Join(root, "usr/bin/myapp-alias")))

	in := testInputs()
	in.Files = append(in.Files,
		OwnedFile{Package: "myapp", Path: "/usr/bin/myapp-openssl"},
		OwnedFile{Package: "myapp", Path: "/usr/bin/myapp-alias"},
	)
	plan, err := BuildPlan(root, in)
	assert.NilError(t, err)

	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/openssl"), KindState))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp-openssl"), KindState))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp"), KindApp))
	assert.Check(t, cmp.Equal(groupOf(t, plan, "/usr/bin/myapp-alias"), KindApp))

	// Group counters follow the moved paths.
	total := 0
	for _, g := range plan.Groups {
		total += g.Paths
	}
	assert.Check(t, cmp.Equal(total, len(plan.Assign)))
}
