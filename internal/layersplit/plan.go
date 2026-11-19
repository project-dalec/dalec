// Package layersplit splits an installed container root filesystem into one
// layer per package set plus a final "state" layer:
//
//   - base: the distro's mandatory base packages and everything they depend on
//   - deps: the other packages from the distro repositories, which are
//     installed as runtime dependencies of the spec packages
//   - app: the packages built from the spec (or passed in as prebuilt
//     packages)
//   - state: the package database and any file that is not owned by a
//     package, is a %ghost file, or was modified after install (as reported by
//     the package manager's verify command)
//
// Layers are ordered from the least to the most frequently changing set, so a
// change to the spec packages only changes their own layer and the state
// layer, and each layer only depends on the packages in its set.
package layersplit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	// PlanVersion is the version of the plan format.
	PlanVersion = 2

	// KindBase is the layer holding the distro's base packages and their
	// dependencies.
	KindBase = "base"
	// KindDeps is the layer holding the other packages from the distro
	// repositories, installed as runtime dependencies of the spec packages.
	KindDeps = "deps"
	// KindApp is the layer holding the packages built from the spec.
	KindApp = "app"
	// KindState is the final layer holding the package database and files
	// that are generated or modified during install.
	KindState = "state"
)

// kinds lists the layer kinds in layer order.
var kinds = []string{KindBase, KindDeps, KindApp, KindState}

// stateDirs are directories whose whole contents always go to the state layer
// because they hold the package database.
var stateDirs = []string{
	"/var/lib/rpm",
	"/usr/lib/sysimage/rpm",
	"/var/lib/dpkg",
}

// Group is one layer of the split.
type Group struct {
	Kind     string   `json:"kind"`
	Packages []string `json:"packages,omitempty"`
	// Size is the total size in bytes of regular files in the group.
	Size int64 `json:"size"`
	// Paths is the number of filesystem entries in the group.
	Paths int `json:"paths"`
}

// HistoryCreatedBy returns a human readable description of the layer of g.
func (g Group) HistoryCreatedBy() string {
	pkgs := strings.Join(g.Packages, ", ")
	switch g.Kind {
	case KindBase:
		return "dalec: base packages: " + pkgs
	case KindDeps:
		return "dalec: runtime dependencies: " + pkgs
	case KindApp:
		return "dalec: packages: " + pkgs
	default:
		return "dalec: package database and generated files"
	}
}

// Plan is the result of classifying a root filesystem.
type Plan struct {
	Version int `json:"version"`
	// Groups lists the layers in order. Package sets without any files are
	// omitted; the state layer is always last.
	Groups []Group `json:"groups"`
	// Assign maps each path (absolute, relative to the root) to the index of
	// its group in Groups. It is written to a separate file, see [WriteAssign].
	Assign map[string]int `json:"-"`
}

// Inputs holds the package manager data used to classify files.
type Inputs struct {
	// Packages is the set of installed package names.
	Packages map[string]bool
	// Deps describes the dependencies between installed packages.
	Deps Deps
	// Files lists files owned by installed packages.
	Files []OwnedFile
	// Modified lists paths reported as changed since install by the package
	// manager's verify command.
	Modified []string
	// App is the set of package names built from the spec.
	App map[string]bool
	// Base is the set of the distro's mandatory base package names. Their
	// dependencies are part of the base set as well.
	Base map[string]bool
	// InstalledAt maps a package to when its files were unpacked, in seconds
	// since the Unix epoch. Files of packages from the distro repositories
	// with a timestamp from then or later (for example created by a
	// maintainer script) are treated as modified. It is used for package
	// managers whose verify command does not check timestamps; packages
	// without an entry are not checked.
	InstalledAt map[string]int64
}

// OwnedFile is a file owned by a package.
type OwnedFile struct {
	Package string
	Path    string
	Ghost   bool
}

// BuildPlan walks root and assigns every entry to a group.
func BuildPlan(root string, in Inputs) (*Plan, error) {
	// Resolving a path walks its parent directories; cache the canonical
	// parent of each directory since most files share their parents.
	parents := make(map[string]string)
	resolve := func(p string) (string, error) {
		if slices.Contains(splitPath(p), "..") {
			// ".." depends on how the parents resolve; don't cache.
			return ResolveInRoot(root, p)
		}
		p = path.Clean("/" + p)
		dir := path.Dir(p)
		if c, ok := parents[dir]; ok {
			return path.Join(c, path.Base(p)), nil
		}
		r, err := ResolveInRoot(root, p)
		if err != nil {
			return "", err
		}
		parents[dir] = path.Dir(r)
		return r, nil
	}

	owners := make(map[string][]string)
	ghost := make(map[string]bool)
	var late []string
	for _, f := range in.Files {
		p, err := resolve(f.Path)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", f.Path, err)
		}
		if f.Ghost {
			ghost[p] = true
			continue
		}
		owners[p] = append(owners[p], f.Package)
		if !in.App[f.Package] && changedAfterInstall(root, p, in.InstalledAt[f.Package]) {
			late = append(late, p)
		}
	}

	modified := make(map[string]bool, len(in.Modified))
	for _, m := range in.Modified {
		p, err := resolve(m)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", m, err)
		}
		modified[p] = true
	}
	for _, p := range late {
		modified[p] = true
	}

	fileOwners := func(p string) []string {
		r, err := resolve(p)
		if err != nil {
			return nil
		}
		return owners[r]
	}
	kindOf := Classify(in, fileOwners)
	rank := func(pkg string) int {
		return slices.Index(kinds, kindOf[pkg])
	}

	type acc struct {
		Group
		pkgs map[string]bool
	}
	groups := make(map[string]*acc, len(kinds))
	for _, k := range kinds {
		groups[k] = &acc{Group: Group{Kind: k}, pkgs: make(map[string]bool)}
	}
	for pkg, k := range kindOf {
		groups[k].pkgs[pkg] = true
	}

	type inode struct{ dev, ino uint64 }
	type linked struct {
		path string
		size int64
	}
	hardlinks := make(map[inode][]linked)

	assignKind := make(map[string]string)
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		p := "/" + filepath.ToSlash(rel)

		kind := KindState
		if !inStateDir(p) && !ghost[p] && !modified[p] {
			// A path owned by packages of several sets goes to the
			// earliest of those layers.
			best := -1
			for _, pkg := range owners[p] {
				if r := rank(pkg); r >= 0 && (best < 0 || r < best) {
					best = r
				}
			}
			if best >= 0 {
				kind = kinds[best]
			}
		}

		g := groups[kind]
		g.Paths++
		if !d.IsDir() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			var size int64
			if d.Type().IsRegular() {
				size = fi.Size()
			}
			g.Size += size
			// Any non-directory can have hard links, including symlinks and
			// device nodes.
			if dev, ino, nlink, ok := inodeOf(fi); ok && nlink > 1 {
				k := inode{dev, ino}
				hardlinks[k] = append(hardlinks[k], linked{p, size})
			}
		}
		assignKind[p] = kind
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Hardlinks only survive within a single layer. When the paths of one
	// file land in different groups, move all of them to the state layer so
	// they stay linked.
	for _, paths := range hardlinks {
		split := false
		for _, l := range paths[1:] {
			if assignKind[l.path] != assignKind[paths[0].path] {
				split = true
				break
			}
		}
		if !split {
			continue
		}
		for _, l := range paths {
			old := groups[assignKind[l.path]]
			old.Paths--
			old.Size -= l.size
			state := groups[KindState]
			state.Paths++
			state.Size += l.size
			assignKind[l.path] = KindState
		}
	}

	plan := &Plan{Version: PlanVersion, Assign: make(map[string]int, len(assignKind))}
	index := make(map[string]int, len(kinds))
	for _, k := range kinds {
		g := groups[k]
		if g.Paths == 0 && k != KindState {
			continue
		}
		g.Packages = sortedKeys(g.pkgs)
		index[k] = len(plan.Groups)
		plan.Groups = append(plan.Groups, g.Group)
	}
	for p, k := range assignKind {
		plan.Assign[p] = index[k]
	}
	return plan, nil
}

// Classify returns the layer kind of every installed package: spec packages
// are [KindApp], the base packages and every package they depend on
// (directly or indirectly) are [KindBase], and all other packages are
// [KindDeps]. fileOwners returns the packages owning a path, to resolve
// dependencies on files.
func Classify(in Inputs, fileOwners func(string) []string) map[string]string {
	out := make(map[string]string, len(in.Packages))
	for pkg := range in.Packages {
		switch {
		case in.App[pkg]:
			out[pkg] = KindApp
		default:
			out[pkg] = KindDeps
		}
	}

	var queue []string
	add := func(pkg string) {
		if out[pkg] == KindDeps {
			out[pkg] = KindBase
			queue = append(queue, pkg)
		}
	}
	for _, pkg := range sortedKeys(in.Base) {
		add(pkg)
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		for _, clause := range in.Deps.Requires[pkg] {
			// Every installed alternative is included, so the result does
			// not depend on which one the package manager picked.
			for _, c := range clause {
				for _, p := range in.Deps.providers(c, in.Packages, fileOwners) {
					add(p)
				}
			}
		}
	}
	return out
}

// changedAfterInstall reports whether the entry p in root, a non-directory
// owned by a package installed at installedAt, was modified when or after the
// package was installed. Files unpacked from a package from the distro
// repositories keep the much older timestamps recorded in the package, so a
// timestamp from the install means the file was created or changed by the
// install (for example by a maintainer script).
func changedAfterInstall(root, p string, installedAt int64) bool {
	if installedAt == 0 {
		return false
	}
	fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.ModTime().Unix() >= installedAt
}

func inStateDir(p string) bool {
	for _, d := range stateDirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResolveInRoot returns the canonical form of the absolute path p inside root:
// every symlink in a parent directory is resolved (scoped to root), while the
// final path component is left as is. Missing components are kept literally.
//
// ".." components are applied to the directory resolved so far, like the
// kernel does, so "/alias/../x" with "/alias -> usr/bin" is "/usr/x". They can
// never move above root.
func ResolveInRoot(root, p string) (string, error) {
	const maxLinks = 255

	pending := splitPath(p)
	cur := "/"
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		if part == ".." {
			cur = path.Dir(cur)
			continue
		}
		next := path.Join(cur, part)
		if len(pending) == 0 {
			return next, nil
		}

		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(next)))
		if err != nil {
			if os.IsNotExist(err) {
				// Keep going through the missing directory: a later ".." can
				// lead back to existing directories whose symlinks must still
				// be resolved inside root.
				cur = next
				continue
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}

		links++
		if links > maxLinks {
			return "", fmt.Errorf("too many levels of symbolic links in %s", p)
		}
		target, err := os.Readlink(filepath.Join(root, filepath.FromSlash(next)))
		if err != nil {
			return "", err
		}
		// Absolute targets restart at root; relative targets continue from
		// the directory containing the link (cur).
		if path.IsAbs(target) {
			cur = "/"
		}
		pending = append(splitPath(target), pending...)
	}
	return cur, nil
}

// splitPath splits p into its components, dropping empty and "." components.
// ".." components are kept so they can be resolved against symlinks.
func splitPath(p string) []string {
	var out []string
	for _, part := range strings.Split(p, "/") {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

// ParseRPMFiles parses lines of
// `rpm -qa --qf '[%{=NAME}\t%{FILESTATES:fstate}\t%{FILEFLAGS:fflags}\t%{FILENAMES}\n]'`.
// Files that are not installed on disk are skipped.
func ParseRPMFiles(r io.Reader) ([]OwnedFile, error) {
	var out []OwnedFile
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) != 4 {
			return nil, fmt.Errorf("malformed rpm file line: %q", line)
		}
		switch parts[1] {
		case "normal", "replaced":
		default:
			// "not installed" (for example docs excluded with --nodocs),
			// "net shared", "wrong color" or "missing": not on disk.
			continue
		}
		out = append(out, OwnedFile{
			Package: parts[0],
			Path:    parts[3],
			Ghost:   strings.Contains(parts[2], "g"),
		})
	}
	return out, sc.Err()
}

// ParseRPMVerify parses the output of `rpm -Va` and returns paths that differ
// from the installed package. Missing files are skipped.
func ParseRPMVerify(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "missing") {
			continue
		}
		i := strings.Index(line, " /")
		if i < 0 {
			continue
		}
		out = append(out, line[i+1:])
	}
	return out, sc.Err()
}

// ParseNames parses a newline separated list of names into a set.
func ParseNames(r io.Reader) (map[string]bool, error) {
	out := make(map[string]bool)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if s := strings.TrimSpace(sc.Text()); s != "" {
			out[s] = true
		}
	}
	return out, sc.Err()
}

// WriteAssign writes the path assignments as NUL separated "index\0path\0" records.
func WriteAssign(w io.Writer, assign map[string]int) error {
	paths := make([]string, 0, len(assign))
	for p := range assign {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	bw := bufio.NewWriter(w)
	for _, p := range paths {
		if _, err := fmt.Fprintf(bw, "%d\x00%s\x00", assign[p], p); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// ReadAssign reads assignments written by [WriteAssign].
func ReadAssign(r io.Reader) (map[string]int, error) {
	dt, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	fields := bytes.Split(bytes.TrimSuffix(dt, []byte{0}), []byte{0})
	if len(dt) == 0 {
		return map[string]int{}, nil
	}
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("malformed assignment file")
	}
	out := make(map[string]int, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		idx, err := strconv.Atoi(string(fields[i]))
		if err != nil {
			return nil, fmt.Errorf("malformed assignment index %q: %w", fields[i], err)
		}
		out[string(fields[i+1])] = idx
	}
	return out, nil
}

// MarshalPlan returns the JSON encoding of the plan groups.
func MarshalPlan(p *Plan) ([]byte, error) {
	return json.MarshalIndent(p, "", "  ")
}

// UnmarshalPlan parses a plan written by [MarshalPlan].
func UnmarshalPlan(dt []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(dt, &p); err != nil {
		return nil, err
	}
	if p.Version != PlanVersion {
		return nil, fmt.Errorf("unsupported layer plan version %d", p.Version)
	}
	if len(p.Groups) == 0 || p.Groups[len(p.Groups)-1].Kind != KindState {
		return nil, fmt.Errorf("layer plan must end with a state layer")
	}
	return &p, nil
}
