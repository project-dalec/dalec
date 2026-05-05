package deb

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"path/filepath"
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

	for k, v := range w.Spec.Build.Env {
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

	checkArtifactPerms := func(artifacts *dalec.Artifacts) bool {
		if artifacts == nil {
			return false
		}
		return checkPerms(artifacts.Binaries) ||
			checkPerms(artifacts.ConfigFiles) ||
			checkPerms(artifacts.Manpages) ||
			checkPerms(artifacts.Headers) ||
			checkPerms(artifacts.Licenses) ||
			checkPerms(artifacts.Docs) ||
			checkPerms(artifacts.Libs) ||
			checkPerms(artifacts.Libexec) ||
			checkPerms(artifacts.DataDirs) ||
			checkDirPerms(artifacts.Directories.GetConfig()) ||
			checkDirPerms(artifacts.Directories.GetState())
	}

	artifacts := w.GetArtifacts(w.target)
	fixPerms := checkArtifactPerms(&artifacts)

	// Also check subpackage artifacts
	if !fixPerms {
		packages := w.Spec.GetSubPackages(w.target)
		for _, pkg := range packages {
			if checkArtifactPerms(pkg.Artifacts) {
				fixPerms = true
				break
			}
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

	artifacts := w.GetArtifacts(w.target)
	units := artifacts.Systemd.GetUnits()

	// Collect subpackage units
	type pkgUnits struct {
		pkgName string
		units   map[string]dalec.SystemdUnitConfig
	}
	var subPkgUnits []pkgUnits

	packages := w.Spec.GetSubPackages(w.target)
	if len(packages) > 0 {
		keys := dalec.SortMapKeys(packages)
		for _, key := range keys {
			pkg := packages[key]
			if pkg.Artifacts == nil {
				continue
			}
			subUnits := pkg.Artifacts.Systemd.GetUnits()
			if len(subUnits) > 0 {
				subPkgUnits = append(subPkgUnits, pkgUnits{
					pkgName: pkg.ResolvedName(w.Spec.Name, key),
					units:   subUnits,
				})
			}
		}
	}

	if len(units) == 0 && len(subPkgUnits) == 0 {
		return b, nil
	}

	b.WriteString("override_dh_installsystemd:\n")

	var includeCustomEnable bool

	// Primary package units
	if len(units) > 0 {
		grouped := groupUnitsByBaseName(units)
		sorted := dalec.SortMapKeys(grouped)

		for _, basename := range sorted {
			grouping := grouped[basename]

			needsCustomEnable := requiresCustomEnable(grouping)
			if needsCustomEnable {
				includeCustomEnable = true
			}

			firstKey := maps.Keys(grouping)[0]
			enable := grouping[firstKey].Enable

			b.WriteString("\tdh_installsystemd --name=" + basename)
			if !enable || needsCustomEnable {
				b.WriteString(" --no-enable")
			}
			b.WriteString("\n")
		}
	}

	// Subpackage units
	for _, su := range subPkgUnits {
		grouped := groupUnitsByBaseName(su.units)
		sorted := dalec.SortMapKeys(grouped)

		for _, basename := range sorted {
			grouping := grouped[basename]

			needsCustomEnable := requiresCustomEnable(grouping)
			if needsCustomEnable {
				includeCustomEnable = true
			}

			firstKey := maps.Keys(grouping)[0]
			enable := grouping[firstKey].Enable

			b.WriteString("\tdh_installsystemd -p" + su.pkgName + " --name=" + basename)
			if !enable || needsCustomEnable {
				b.WriteString(" --no-enable")
			}
			b.WriteString("\n")
		}
	}

	if includeCustomEnable {
		b.WriteString("\t[ -f debian/postinst ] || (echo '#!/bin/sh' > debian/postinst; echo 'set -e' >> debian/postinst)\n")
		b.WriteString("\t[ -x debian/postinst ] || chmod +x debian/postinst\n")
		b.WriteString("\tcat debian/dalec/" + customSystemdPostinstFile + " >> debian/postinst\n")
	}

	return b, nil
}

func (w *rulesWrapper) OverrideStrip() fmt.Stringer {
	artifacts := w.Spec.GetArtifacts(w.target)

	buf := &strings.Builder{}

	if artifacts.DisableStrip {
		buf.WriteString("override_dh_strip:\n")
	}
	return buf
}
