package deb

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
	"golang.org/x/exp/maps"
)

var (
	//go:embed templates/debian_rules.tmpl
	rulesTmplContent []byte

	rulesTmpl = template.Must(template.New("rules").Parse(string(rulesTmplContent)))
)

func Rules(spec *dalec.Spec, in llb.State, dir, target string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	buf := bytes.NewBuffer(nil)

	if dir == "" {
		dir = "debian"
	}

	if err := WriteRules(spec, buf, target); err != nil {
		return llb.Scratch(), err
	}

	return in.
			File(llb.Mkdir(dir, 0o755, llb.WithParents(true)), opts...).
			File(llb.Mkfile(filepath.Join(dir, "rules"), 0o700, buf.Bytes()), opts...),
		nil
}

func WriteRules(spec *dalec.Spec, w io.Writer, target string) error {
	return rulesTmpl.Execute(w, &rulesWrapper{spec, target})
}

type rulesWrapper struct {
	*dalec.Spec
	target string
}

func (w *rulesWrapper) Envs() fmt.Stringer {
	b := &strings.Builder{}

	for k, v := range dalec.SortedMapIter(w.Spec.Build.Env) {
		fmt.Fprintf(b, "export %s := %s\n", k, v)
	}

	if w.Spec.HasGomods() {
		fmt.Fprintf(b, "export %s := $(PWD)/%s\n", "GOMODCACHE", gomodsName)
	}

	if w.Spec.HasCargohomes() {
		fmt.Fprintf(b, "export %s := $(PWD)/%s\n", "CARGO_HOME", cargohomeName)
	}

	if w.Spec.HasPips() {
		// Set up pip environment for build-time installation
		fmt.Fprintf(b, "export %s := $(PWD)/%s\n", "PIP_CACHE_DIR", pipDepsName)
		fmt.Fprintf(b, "export %s := $(PWD)/site-packages:${PYTHONPATH}\n", "PYTHONPATH")
	}

	return b
}

func (w *rulesWrapper) OverridePerms() fmt.Stringer {
	b := &strings.Builder{}

	checkPerms := func(cfgs map[string]dalec.ArtifactConfig) bool {
		for _, cfg := range cfgs {
			if cfg.Permissions.Perm() != 0 {
				return true
			}
		}
		return false
	}

	checkDirPerms := func(dirConfigs map[string]dalec.ArtifactDirConfig) bool {
		for _, cfg := range dirConfigs {
			if cfg.Mode.Perm() != 0 {
				return true
			}
		}
		return false
	}

	checkArtifactPerms := func(artifacts dalec.Artifacts) bool {
		return checkPerms(artifacts.Binaries) ||
			checkPerms(artifacts.ConfigFiles) ||
			checkPerms(artifacts.Manpages) ||
			checkPerms(artifacts.Headers) ||
			checkPerms(artifacts.Licenses) ||
			checkPerms(artifacts.Docs) ||
			checkPerms(artifacts.Libs) ||
			checkPerms(artifacts.Libexec) ||
			checkPerms(artifacts.Opt) ||
			checkPerms(artifacts.DataDirs) ||
			checkDirPerms(artifacts.Directories.GetConfig()) ||
			checkDirPerms(artifacts.Directories.GetState())
	}

	var fixPerms bool
	for _, pkg := range resolvePackages(w.Spec, w.target) {
		if checkArtifactPerms(pkg.artifacts) {
			fixPerms = true
			break
		}
	}

	if fixPerms {
		// Normally this should be `execute_after_dh_fixperms`, however this doesn't
		// work on Ubuntu 18.04.
		// Instead we need to override dh_fixperms and run it ourselves and then
		// our extra script.
		b.WriteString("override_dh_fixperms:\n")
		b.WriteString("\tdh_fixperms\n")
		b.WriteString("\tdebian/dalec/fix_perms.sh\n\n")
	}

	return b
}

// groupUnitsByBaseName indexes the provided list by the unit basename.
// A unit basename is the name of the unit without the suffix (e.g. ".service", ".socket", etc).
// The nested map is key'd on the fully resolved unit name.
func groupUnitsByBaseName(ls map[string]dalec.SystemdUnitConfig) map[string]map[string]dalec.SystemdUnitConfig {
	idx := make(map[string]map[string]dalec.SystemdUnitConfig)
	for k, v := range ls {
		base, suffix := v.SplitName(k)
		if idx[base] == nil {
			idx[base] = make(map[string]dalec.SystemdUnitConfig)
		}
		idx[base][base+"."+suffix] = v
	}

	return idx
}

func (w *rulesWrapper) OverrideSystemd() (fmt.Stringer, error) {
	b := &strings.Builder{}

	packages := resolvePackages(w.Spec, w.target)
	var hasUnits bool
	for _, pkg := range packages {
		if len(pkg.artifacts.Systemd.GetUnits()) > 0 {
			hasUnits = true
			break
		}
	}

	if !hasUnits {
		return b, nil
	}

	b.WriteString("override_dh_installsystemd:\n")

	// Track which packages need a custom-enable snippet appended to their own
	// maintainer script. The snippet for a package's units must land in that
	// package's postinst, not always in the primary package's postinst.
	var customEnablePartials []customSystemdPartial

	for _, pkg := range packages {
		grouped := groupUnitsByBaseName(pkg.artifacts.Systemd.GetUnits())
		var needsCustomPartial bool
		for basename, grouping := range dalec.SortedMapIter(grouped) {
			needsCustomEnable := requiresCustomEnable(grouping)
			if needsCustomEnable {
				needsCustomPartial = true
			}

			firstKey := maps.Keys(grouping)[0]
			enable := grouping[firstKey].Enable

			b.WriteString("\tdh_installsystemd")
			if !pkg.primary {
				b.WriteString(" -p" + pkg.name)
			}
			b.WriteString(" --name=" + basename)
			if !enable || needsCustomEnable {
				b.WriteString(" --no-enable")
			}
			b.WriteString("\n")
		}

		if needsCustomPartial {
			customEnablePartials = append(customEnablePartials, customSystemdPartial{
				pkgName:   pkg.name,
				isPrimary: pkg.primary,
			})
		}
	}

	for _, partial := range customEnablePartials {
		target := partial.postinstTarget()
		b.WriteString("\t[ -f " + target + " ] || (echo '#!/bin/sh' > " + target + "; echo 'set -e' >> " + target + ")\n")
		b.WriteString("\t[ -x " + target + " ] || chmod +x " + target + "\n")
		b.WriteString("\tcat debian/dalec/" + partial.partialFile() + " >> " + target + "\n")
	}

	return b, nil
}

func (w *rulesWrapper) OverrideStrip() fmt.Stringer {
	artifacts := w.Spec.GetArtifacts(w.target)

	buf := &strings.Builder{}

	if artifacts.DisableStrip {
		buf.WriteString("override_dh_strip:\n")
		// dh_strip_nondeterminism is a separate helper that debhelper runs even
		// when dh_strip is overridden. It fails on some inputs (e.g. Go's corrupt
		// archive test fixtures), so disable it alongside dh_strip.
		buf.WriteString("override_dh_strip_nondeterminism:\n")
	}
	return buf
}

func (w *rulesWrapper) OverrideAutoRequires() fmt.Stringer {
	buf := &strings.Builder{}

	packages := resolvePackages(w.Spec, w.target)
	disabled := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if pkg.artifacts.DisableAutoRequires {
			disabled = append(disabled, pkg.name)
		}
	}

	if len(disabled) == 0 {
		return buf
	}

	buf.WriteString("override_dh_shlibdeps:\n")
	if len(disabled) == len(packages) {
		return buf
	}

	sort.Strings(disabled)
	buf.WriteString("\tdh_shlibdeps")
	for _, name := range disabled {
		fmt.Fprintf(buf, " -N%s", name)
	}
	buf.WriteString("\n")

	return buf
}
