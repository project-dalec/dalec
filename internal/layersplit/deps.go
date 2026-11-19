package layersplit

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// Deps describes the dependencies between installed packages.
type Deps struct {
	// Requires maps a package to its dependencies. Each dependency is a list
	// of alternative capabilities: a package name, a virtual package or RPM
	// capability, or an absolute file path.
	Requires map[string][][]string
	// Provides maps a capability to the packages providing it. Every package
	// implicitly provides its own name.
	Provides map[string][]string
}

func (d *Deps) addRequire(pkg string, alts []string) {
	if len(alts) == 0 {
		return
	}
	if d.Requires == nil {
		d.Requires = make(map[string][][]string)
	}
	d.Requires[pkg] = append(d.Requires[pkg], alts)
}

func (d *Deps) addProvide(capability, pkg string) {
	if capability == "" {
		return
	}
	if d.Provides == nil {
		d.Provides = make(map[string][]string)
	}
	if !slices.Contains(d.Provides[capability], pkg) {
		d.Provides[capability] = append(d.Provides[capability], pkg)
	}
}

// providers returns the installed packages providing capability c, sorted.
func (d *Deps) providers(c string, installed map[string]bool, fileOwners func(string) []string) []string {
	var out []string
	if installed[c] {
		out = append(out, c)
	}
	for _, p := range d.Provides[c] {
		if installed[p] {
			out = append(out, p)
		}
	}
	if strings.HasPrefix(c, "/") && fileOwners != nil {
		for _, p := range fileOwners(c) {
			if installed[p] {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ParseRPMRequires parses lines of `rpm -qa --qf '[%{=NAME}\t%{REQUIRENAME}\n]'`
// into d. rpmlib() requirements are skipped, and every package named in a
// rich (boolean) dependency is treated as an alternative.
func ParseRPMRequires(r io.Reader, d *Deps) error {
	return scanPairs(r, func(pkg, req string) {
		if strings.HasPrefix(req, "rpmlib(") {
			return
		}
		if strings.HasPrefix(req, "(") {
			d.addRequire(pkg, richDepNames(req))
			return
		}
		d.addRequire(pkg, []string{req})
	})
}

// ParseRPMProvides parses lines of `rpm -qa --qf '[%{=NAME}\t%{PROVIDENAME}\n]'`
// into d.
func ParseRPMProvides(r io.Reader, d *Deps) error {
	return scanPairs(r, func(pkg, capability string) {
		d.addProvide(capability, pkg)
	})
}

// richDepOperators are the keywords and comparison operators of RPM rich
// dependencies.
var richDepOperators = map[string]bool{
	"and": true, "or": true, "if": true, "else": true, "with": true, "without": true, "unless": true,
	"<": true, "<=": true, "=": true, ">=": true, ">": true, "<>": true,
}

// richDepNames returns the capability names in an RPM rich dependency such as
// "(foo >= 1.0 or (bar if baz))".
func richDepNames(s string) []string {
	var out []string
	skipVersion := false
	for _, tok := range strings.Fields(s) {
		// Strip the grouping parentheses, but not those that are part of a
		// capability name such as "libc.so.6(GLIBC_2.34)(64bit)".
		tok = strings.TrimLeft(tok, "(")
		for strings.HasSuffix(tok, ")") && strings.Count(tok, ")") > strings.Count(tok, "(") {
			tok = tok[:len(tok)-1]
		}
		switch {
		case tok == "":
		case skipVersion:
			skipVersion = false
		case richDepOperators[tok]:
			skipVersion = strings.ContainsAny(tok, "<=>")
		default:
			out = append(out, tok)
		}
	}
	return out
}

// ParseDpkgDeps parses lines of
// `dpkg-query -W -f='${Package}\t${Pre-Depends}\t${Depends}\t${Recommends}\t${Provides}\n'`
// into d.
func ParseDpkgDeps(r io.Reader, d *Deps) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return fmt.Errorf("malformed dpkg dependency line: %q", line)
		}
		pkg := strings.TrimSpace(parts[0])
		for _, field := range parts[1:4] {
			for _, clause := range strings.Split(field, ",") {
				var alts []string
				for _, alt := range strings.Split(clause, "|") {
					if name := dpkgName(alt); name != "" {
						alts = append(alts, name)
					}
				}
				d.addRequire(pkg, alts)
			}
		}
		for _, p := range strings.Split(parts[4], ",") {
			d.addProvide(dpkgName(p), pkg)
		}
	}
	return sc.Err()
}

// dpkgName returns the package name of a dpkg relation such as
// "libc6:amd64 (>= 2.34)".
func dpkgName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " (["); i >= 0 {
		s = s[:i]
	}
	s, _, _ = strings.Cut(s, ":")
	return s
}

// scanPairs calls fn for every "key\tvalue" line of r.
func scanPairs(r io.Reader, fn func(k, v string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "\t")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || k == "" || v == "" {
			continue
		}
		fn(k, v)
	}
	return sc.Err()
}

// ParseInstalledAt parses "seconds\tname" lines, as written by
// `stat -c '%Y\t%n'` on dpkg's info/<name>.list files: the paths are reduced
// to the package name.
func ParseInstalledAt(r io.Reader) (map[string]int64, error) {
	out := make(map[string]int64)
	var parseErr error
	err := scanPairs(r, func(ts, name string) {
		sec, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			parseErr = fmt.Errorf("malformed install time %q for %s", ts, name)
			return
		}
		name = name[strings.LastIndex(name, "/")+1:]
		name = strings.TrimSuffix(name, ".list")
		name, _, _ = strings.Cut(name, ":")
		out[name] = sec
	})
	if err != nil {
		return nil, err
	}
	return out, parseErr
}
