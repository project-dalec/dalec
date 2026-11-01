package layersplit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	t.Run("sources", func(t *testing.T) {
		in := "libssl3t64\topenssl (3.0.13-0ubuntu3.4)\nbash\t\nlibc6\tglibc\n\n"
		pkgs, err := ParseSources(strings.NewReader(in))
		assert.NilError(t, err)
		assert.Check(t, cmp.DeepEqual(pkgs, map[string]string{"libssl3t64": "openssl", "bash": "bash", "libc6": "glibc"}))
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

func TestRemoveLinks(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	assert.NilError(t, os.WriteFile(victim, []byte("keep"), 0o644))

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"/usr/bin/app":   "app",
		"/etc/file-link": "a regular file",
		"/data/keep":     "x",
	}, map[string]string{
		"/app-link":        "/usr/bin/app",
		"/usr/lib64":       "lib",
		"/escape":          outside, // absolute symlink to a directory outside the root
		"/usr/local/bin/r": "../../bin/app",
	})

	err := RemoveLinks(root, []string{
		"/app-link",                 // symlink: removed
		"usr/local/bin/r",           // relative path: removed
		"/etc/file-link",            // regular file: removed
		"/data",                     // directory: kept
		"/missing",                  // missing: ignored
		"/escape/victim",            // resolves inside root, not to the outside file
		"/missing/../escape/victim", // a missing directory does not skip resolving /escape
		"/../../.." + victim,        // ".." cannot leave the root
		"/" + filepath.Base(outside) + "/../victim",
	})
	assert.NilError(t, err)

	for _, p := range []string{"/app-link", "/usr/local/bin/r", "/etc/file-link"} {
		_, err := os.Lstat(filepath.Join(root, p))
		assert.Check(t, os.IsNotExist(err), p)
	}
	_, err = os.Stat(filepath.Join(root, "data/keep"))
	assert.NilError(t, err, "directories are kept")
	dt, err := os.ReadFile(victim)
	assert.NilError(t, err, "files outside the root must not be removed")
	assert.Check(t, cmp.Equal(string(dt), "keep"))
}

func TestRemoveLinksResolvesDotDotAfterSymlinks(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"/victim": "keep", "/usr/victim": "remove"}, map[string]string{"/alias": "usr/bin"})
	assert.NilError(t, os.MkdirAll(filepath.Join(root, "usr/bin"), 0o755))

	// /alias is /usr/bin, so /alias/../victim is /usr/victim, not /victim.
	assert.NilError(t, RemoveLinks(root, []string{"/alias/../victim"}))

	_, err := os.Stat(filepath.Join(root, "usr/victim"))
	assert.Check(t, os.IsNotExist(err), "/usr/victim should be removed")
	_, err = os.Stat(filepath.Join(root, "victim"))
	assert.NilError(t, err, "/victim must be kept")
}

func TestRemoveLinksNested(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"/usr/bin/hello": "hello", "/usr/share/x/keep": "x"}, map[string]string{
		// /opt/a points at a directory, and /opt/a/b was created through it.
		"/opt/a":         "/usr/share/x",
		"/usr/share/x/b": "/usr/bin/hello",
	})

	// The parent link comes first in sorted order; the nested link must still
	// be removed.
	assert.NilError(t, RemoveLinks(root, []string{"/opt/a", "/opt/a/b"}))

	for _, p := range []string{"/opt/a", "/usr/share/x/b"} {
		_, err := os.Lstat(filepath.Join(root, p))
		assert.Check(t, os.IsNotExist(err), p)
	}
	_, err := os.Stat(filepath.Join(root, "usr/share/x/keep"))
	assert.NilError(t, err)
}

func TestRemoveLinksResolvesBeforeRemoving(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"/opt/data/keep": "x"}, map[string]string{
		"/alias":          "usr/share/link",
		"/usr/share/link": "../../opt/data",
		"/opt/data/x":     "/usr/bin/hello",
	})

	// /alias/x goes through /usr/share/link, which is removed too. It must
	// still remove /opt/data/x, whatever the order.
	for _, order := range [][]string{{"/alias/x", "/usr/share/link"}, {"/usr/share/link", "/alias/x"}} {
		writeTree(t, root, nil, nil)
		_ = os.Remove(filepath.Join(root, "usr/share/link"))
		_ = os.Remove(filepath.Join(root, "opt/data/x"))
		assert.NilError(t, os.Symlink("../../opt/data", filepath.Join(root, "usr/share/link")))
		assert.NilError(t, os.Symlink("/usr/bin/hello", filepath.Join(root, "opt/data/x")))

		assert.NilError(t, RemoveLinks(root, order))
		for _, p := range []string{"/usr/share/link", "/opt/data/x"} {
			_, err := os.Lstat(filepath.Join(root, p))
			assert.Check(t, os.IsNotExist(err), "%s with order %v", p, order)
		}
		_, err := os.Stat(filepath.Join(root, "opt/data/keep"))
		assert.NilError(t, err)
	}
}

func TestPruneTools(t *testing.T) {
	tools := t.TempDir()
	writeTree(t, tools, map[string]string{
		"/usr/bin/sh":         "tools sh",
		"/usr/bin/dpkg":       "tools dpkg",
		"/usr/lib/tools.so":   "tools lib",
		"/etc/shared":         "tools shared",
		"/etc/tools/conf":     "tools conf",
		"/var/lib/tools/data": "tools data",
		"/opt/hidden/file":    "tools hidden",
	}, map[string]string{"/bin": "usr/bin"})

	lower := t.TempDir()
	writeTree(t, lower, map[string]string{
		"/usr/bin/app": "app",
		"/etc/shared":  "image shared",
	}, map[string]string{"/bin": "usr/bin", "/opt": "usr"})

	// root is lower merged on top of tools after installing a package.
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"/usr/bin/app":         "app",
		"/usr/bin/sh":          "tools sh",
		"/usr/bin/dpkg":        "tools dpkg",
		"/usr/lib/tools.so":    "reinstalled by an updated package",
		"/usr/lib/new.so":      "new from the update",
		"/etc/shared":          "image shared",
		"/etc/tools/conf":      "tools conf",
		"/var/lib/tools/data":  "tools data",
		"/var/lib/tools/new":   "created by a maintainer script",
		"/usr/share/new/file1": "new",
	}, map[string]string{"/bin": "usr/bin", "/opt": "usr"})
	// Copies of the tools' files keep their modification times, like the
	// merged root filesystem does. One of them is then rewritten by a
	// maintainer script, with the same size.
	for _, p := range []string{"/usr/bin/sh", "/usr/bin/dpkg", "/usr/lib/tools.so", "/etc/tools/conf", "/var/lib/tools/data"} {
		fi, err := os.Stat(filepath.Join(tools, p))
		assert.NilError(t, err)
		assert.NilError(t, os.Chtimes(filepath.Join(root, p), fi.ModTime(), fi.ModTime()))
	}
	writeTree(t, tools, map[string]string{"/var/lib/dpkg/info/newdep.list": "old"}, nil)
	old := time.Unix(1700000000, 0)
	assert.NilError(t, os.Chtimes(filepath.Join(tools, "/var/lib/dpkg/info/newdep.list"), old, old))
	writeTree(t, root, map[string]string{"/var/lib/dpkg/info/newdep.list": "new"}, nil)

	assert.NilError(t, PruneTools(root, lower, tools, []string{"/lib/../usr/lib/tools.so", "/bin/unrelated"}))

	exists := func(p string) bool {
		_, err := os.Lstat(filepath.Join(root, p))
		return err == nil
	}
	for _, p := range []string{"/usr/bin/sh", "/usr/bin/dpkg", "/etc/tools/conf", "/etc/tools", "/var/lib/tools/data"} {
		assert.Check(t, !exists(p), "%s should be removed", p)
	}
	for _, p := range []string{"/var/lib/dpkg/info/newdep.list", "/usr/bin/app", "/usr/lib/tools.so", "/usr/lib/new.so", "/etc/shared", "/var/lib/tools/new", "/usr/share/new/file1", "/bin", "/opt"} {
		assert.Check(t, exists(p), "%s should be kept", p)
	}
}
