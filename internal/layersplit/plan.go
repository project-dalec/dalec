// Package layersplit splits an installed container root filesystem into one
// layer per package (or group of related packages) plus a final "state" layer.
//
// The state layer holds the package database and any file that is not owned by
// a package, is a %ghost file, or was modified after install (as reported by the
// package manager's verify command). Every other file goes into the layer of
// the package that owns it. This keeps each package layer a function of the
// package alone, so updating one package only changes its own layer and the
// state layer.
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
	"sort"
	"strconv"
	"strings"
)

const (
	// PlanVersion is the version of the plan format and of the layer history
	// comment convention.
	PlanVersion = 1

	// KindOS is a layer holding files of packages from the distro repositories.
	KindOS = "os"
	// KindApp is a layer holding files of a package built from the spec.
	KindApp = "app"
	// KindState is the final layer holding the package database and files
	// that are generated or modified during install.
	KindState = "state"

	// MiscGroupName is the name of the group that collects packages which did
	// not fit in the layer budget.
	MiscGroupName = "misc"

	// DefaultMaxLayers is the default maximum number of layers produced,
	// including the state layer.
	DefaultMaxLayers = 64

	// HistoryCommentPrefix prefixes the image history comment of every layer
	// written by this package. Tools (for example image patchers) can use the
	// comment to find which packages a layer holds.
	HistoryCommentPrefix = "dalec.layer.v1"

	// LabelImageLayers is the image label set on images using per-package layers.
	LabelImageLayers = "dev.dalec.image.layers"

	// LabelSpecPackages is the image label listing every installed package
	// built from the spec (comma separated), including packages without files
	// of their own, which have no layer.
	LabelSpecPackages = "dev.dalec.image.spec-packages"
)

// stateDirs are directories whose whole contents always go to the state layer
// because they hold the package database.
var stateDirs = []string{
	"/var/lib/rpm",
	"/usr/lib/sysimage/rpm",
	"/var/lib/dpkg",
}

// Group is one layer of the split.
type Group struct {
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Packages []string `json:"packages,omitempty"`
	// Size is the total size in bytes of regular files in the group.
	Size int64 `json:"size"`
	// Paths is the number of filesystem entries in the group.
	Paths int `json:"paths"`
}

// HistoryComment returns the image history comment for the layer of g.
func (g Group) HistoryComment() string {
	s := HistoryCommentPrefix + " kind=" + g.Kind + " name=" + g.Name
	if len(g.Packages) > 0 {
		s += " packages=" + strings.Join(g.Packages, ",")
	}
	return s
}

// HistoryCreatedBy returns a human readable description of the layer of g.
func (g Group) HistoryCreatedBy() string {
	if g.Kind == KindState {
		return "dalec: package database and generated files"
	}
	return "dalec: files from " + strings.Join(g.Packages, ", ")
}

// ParseHistoryComment parses a history comment written by [Group.HistoryComment].
func ParseHistoryComment(s string) (Group, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 || fields[0] != HistoryCommentPrefix {
		return Group{}, false
	}

	var g Group
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		switch k {
		case "kind":
			g.Kind = v
		case "name":
			g.Name = v
		case "packages":
			if v != "" {
				g.Packages = strings.Split(v, ",")
			}
		}
	}
	if g.Kind == "" || g.Name == "" {
		return Group{}, false
	}
	return g, true
}

// Plan is the result of classifying a root filesystem.
type Plan struct {
	Version int     `json:"version"`
	Groups  []Group `json:"groups"`
	// SpecPackages lists every installed package built from the spec, sorted.
	SpecPackages []string `json:"specPackages,omitempty"`
	// Assign maps each path (absolute, relative to the root) to the index of
	// its group in Groups. It is written to a separate file, see [WriteAssign].
	Assign map[string]int `json:"-"`
}

// Inputs holds the package manager data used to classify files.
type Inputs struct {
	// Packages maps an installed package name to its group key, for example
	// the source package name.
	Packages map[string]string
	// Files lists files owned by installed packages.
	Files []OwnedFile
	// Modified lists paths reported as changed since install by the package
	// manager's verify command.
	Modified []string
	// App is the set of package names built from the spec.
	App map[string]bool
}

// OwnedFile is a file owned by a package.
type OwnedFile struct {
	Package string
	Path    string
	Ghost   bool
}

// Options configures [BuildPlan].
type Options struct {
	// MaxLayers is the maximum number of layers, including the state layer.
	// Zero means [DefaultMaxLayers].
	MaxLayers int
}

// BuildPlan walks root and assigns every entry to a group.
func BuildPlan(root string, in Inputs, opts Options) (*Plan, error) {
	maxLayers := opts.MaxLayers
	if maxLayers == 0 {
		maxLayers = DefaultMaxLayers
	}

	// Resolving a path walks its parent directories; cache the canonical
	// parent of each directory since most files share their parents.
	parents := make(map[string]string)
	resolve := func(p string) (string, error) {
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
	}

	modified := make(map[string]bool, len(in.Modified))
	for _, m := range in.Modified {
		p, err := resolve(m)
		if err != nil {
			return nil, fmt.Errorf("resolving %s: %w", m, err)
		}
		modified[p] = true
	}

	groupKey := func(pkg string) string {
		if in.App[pkg] {
			return KindApp + ":" + pkg
		}
		if k := in.Packages[pkg]; k != "" {
			return KindOS + ":" + k
		}
		return KindOS + ":" + pkg
	}

	type acc struct {
		Group
		pkgs map[string]bool
	}
	groups := make(map[string]*acc)
	getGroup := func(key, kind, name string) *acc {
		g, ok := groups[key]
		if !ok {
			g = &acc{Group: Group{Name: name, Kind: kind}, pkgs: make(map[string]bool)}
			groups[key] = g
		}
		return g
	}
	stateKey := KindState + ":"
	getGroup(stateKey, KindState, KindState)

	for pkg := range in.Packages {
		key := groupKey(pkg)
		kind, name, _ := strings.Cut(key, ":")
		getGroup(key, kind, name).pkgs[pkg] = true
	}
	for pkg := range in.App {
		key := groupKey(pkg)
		kind, name, _ := strings.Cut(key, ":")
		getGroup(key, kind, name).pkgs[pkg] = true
	}

	type inode struct{ dev, ino uint64 }
	type linked struct {
		path string
		size int64
	}
	hardlinks := make(map[inode][]linked)

	assignKey := make(map[string]string)
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

		key := stateKey
		if !inStateDir(p) && !ghost[p] && !modified[p] {
			if pkgs := owners[p]; len(pkgs) > 0 {
				keys := make([]string, 0, len(pkgs))
				for _, pkg := range pkgs {
					keys = append(keys, groupKey(pkg))
				}
				sort.Strings(keys)
				key = keys[0]
			}
		}

		kind, name, _ := strings.Cut(key, ":")
		if kind == KindState {
			name = KindState
		}
		g := getGroup(key, kind, name)
		g.Paths++
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			g.Size += fi.Size()
			if dev, ino, nlink, ok := inodeOf(fi); ok && nlink > 1 {
				k := inode{dev, ino}
				hardlinks[k] = append(hardlinks[k], linked{p, fi.Size()})
			}
		}
		assignKey[p] = key
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
			if assignKey[l.path] != assignKey[paths[0].path] {
				split = true
				break
			}
		}
		if !split {
			continue
		}
		for _, l := range paths {
			old := groups[assignKey[l.path]]
			old.Paths--
			old.Size -= l.size
			state := groups[stateKey]
			state.Paths++
			state.Size += l.size
			assignKey[l.path] = stateKey
		}
	}

	var osKeys, appKeys []string
	for key, g := range groups {
		if key == stateKey || g.Paths == 0 {
			continue
		}
		if g.Kind == KindApp {
			appKeys = append(appKeys, key)
		} else {
			osKeys = append(osKeys, key)
		}
	}
	sort.Strings(appKeys)

	// Keep the layer count within budget by folding the smallest OS groups
	// into a single misc group. App groups always get their own layer.
	budget := maxLayers - 1 - len(appKeys)
	if budget < 1 {
		return nil, fmt.Errorf("layer budget of %d is too small for %d spec packages", maxLayers, len(appKeys))
	}
	if len(osKeys) > budget {
		sort.Slice(osKeys, func(i, j int) bool {
			gi, gj := groups[osKeys[i]], groups[osKeys[j]]
			if gi.Size != gj.Size {
				return gi.Size > gj.Size
			}
			return osKeys[i] < osKeys[j]
		})
		miscKey := KindOS + ":" + MiscGroupName + "\x00"
		misc := getGroup(miscKey, KindOS, MiscGroupName)
		folded := make(map[string]bool, len(osKeys)-(budget-1))
		for _, key := range osKeys[budget-1:] {
			g := groups[key]
			misc.Paths += g.Paths
			misc.Size += g.Size
			for pkg := range g.pkgs {
				misc.pkgs[pkg] = true
			}
			folded[key] = true
			delete(groups, key)
		}
		for p, k := range assignKey {
			if folded[k] {
				assignKey[p] = miscKey
			}
		}
		osKeys = append(osKeys[:budget-1:budget-1], miscKey)
	}
	sort.Strings(osKeys)

	ordered := append(append(osKeys, appKeys...), stateKey)
	plan := &Plan{Version: PlanVersion, Assign: make(map[string]int, len(assignKey))}
	for pkg := range in.App {
		if _, installed := in.Packages[pkg]; installed {
			plan.SpecPackages = append(plan.SpecPackages, pkg)
		}
	}
	sort.Strings(plan.SpecPackages)
	index := make(map[string]int, len(ordered))
	for i, key := range ordered {
		g := groups[key]
		g.Packages = sortedKeys(g.pkgs)
		plan.Groups = append(plan.Groups, g.Group)
		index[key] = i
	}
	for p, key := range assignKey {
		plan.Assign[p] = index[key]
	}
	return plan, nil
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
func ResolveInRoot(root, p string) (string, error) {
	const maxLinks = 255

	pending := splitPath(p)
	cur := "/"
	links := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		next := path.Join(cur, part)
		if len(pending) == 0 {
			return next, nil
		}

		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(next)))
		if err != nil {
			if os.IsNotExist(err) {
				return path.Join(append([]string{next}, pending...)...), nil
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
		if !path.IsAbs(target) {
			target = path.Join(cur, target)
		}
		pending = append(splitPath(target), pending...)
		cur = "/"
	}
	return cur, nil
}

func splitPath(p string) []string {
	var out []string
	for _, part := range strings.Split(path.Clean("/"+p), "/") {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// SourcePackageName returns the source package name from an RPM SOURCERPM
// value such as "openssl-3.3.2-1.azl3.src.rpm".
func SourcePackageName(srpm string) string {
	s := strings.TrimSuffix(srpm, ".src.rpm")
	s = strings.TrimSuffix(s, ".nosrc.rpm")
	for range 2 {
		i := strings.LastIndex(s, "-")
		if i <= 0 {
			return ""
		}
		s = s[:i]
	}
	return s
}

// ParseRPMPackages parses lines of `rpm -qa --qf '%{NAME}\t%{SOURCERPM}\n'`
// and returns a map of package name to source package name.
func ParseRPMPackages(r io.Reader) (map[string]string, error) {
	out := make(map[string]string)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		name, srpm, _ := strings.Cut(line, "\t")
		src := SourcePackageName(srpm)
		if src == "" {
			src = name
		}
		out[name] = src
	}
	return out, sc.Err()
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
