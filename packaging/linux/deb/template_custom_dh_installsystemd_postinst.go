package deb

import (
	"bytes"
	_ "embed"
	"io"
	"text/template"

	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

var (
	//go:embed templates/custom_enable_postinst.tmpl
	customEnableTmplContent []byte
	//go:embed templates/custom_noenable_postinst.tmpl
	customNoEnableTmplContent []byte
	//go:embed templates/custom_start_postinst.tmpl
	customStartTmplContent []byte

	customEnableTmpl   = template.Must(template.New("enable").Parse(string(customEnableTmplContent)))
	customNoEnableTmpl = template.Must(template.New("no-enable").Parse(string(customNoEnableTmplContent)))
	customStartTmpl    = template.Must(template.New("start").Parse(string(customStartTmplContent)))
)

// customSystemdPartial holds the generated custom-enable postinst snippet for a
// single package (primary or subpackage). The snippet must be appended to that
// package's own maintainer script so that units ship and enable in the same
// package.
type customSystemdPartial struct {
	// pkgName is the resolved package name that owns the units.
	pkgName string
	// isPrimary is true for the primary package.
	isPrimary bool
	// content is the custom-enable postinst snippet.
	content []byte
}

// partialFile returns the filename (under debian/dalec/) the snippet is written to.
func (p customSystemdPartial) partialFile() string {
	if p.isPrimary {
		return customSystemdPostinstFile
	}
	return p.pkgName + "." + customSystemdPostinstFile
}

// postinstTarget returns the maintainer script the snippet must be appended to.
func (p customSystemdPartial) postinstTarget() string {
	if p.isPrimary {
		return "debian/postinst"
	}
	return "debian/" + p.pkgName + ".postinst"
}

// This is used to generate a postinst (or at least part of a postinst) for the
// case where we have a mix of enabled/disabled units with the same basename.
// For all units that need this, the `dh_installsystemd` command should be
// executed with the `--no-enable` option.
// This handles enabled or not enabled for this special case instead of using
// the postinst provided by `dh_installsystemd` without `--no-eable` set.
//
// A snippet is generated per package (primary and each subpackage) so that each
// snippet can be appended to the maintainer script of the package that actually
// ships the units, rather than always landing in the primary package's postinst.
func customDHInstallSystemdPostinst(spec *dalec.Spec, target string) ([]customSystemdPartial, error) {
	var partials []customSystemdPartial

	// Primary package units
	artifacts := spec.GetArtifacts(target)
	buf := bytes.NewBuffer(nil)
	if err := writeCustomEnableForUnits(buf, artifacts.Systemd.GetUnits()); err != nil {
		return nil, err
	}
	if buf.Len() > 0 {
		partials = append(partials, customSystemdPartial{
			pkgName:   spec.Name,
			isPrimary: true,
			content:   buf.Bytes(),
		})
	}

	// Subpackage units — each gets its own snippet keyed by resolved name.
	packages := spec.GetSubPackages(target)
	if len(packages) > 0 {
		keys := dalec.SortMapKeys(packages)
		for _, key := range keys {
			pkg := packages[key]
			if pkg.Artifacts == nil {
				continue
			}
			subBuf := bytes.NewBuffer(nil)
			if err := writeCustomEnableForUnits(subBuf, pkg.Artifacts.Systemd.GetUnits()); err != nil {
				return nil, errors.Wrapf(err, "subpackage %s", key)
			}
			if subBuf.Len() > 0 {
				partials = append(partials, customSystemdPartial{
					pkgName: pkg.ResolvedName(spec.Name, key),
					content: subBuf.Bytes(),
				})
			}
		}
	}

	return partials, nil
}

func writeCustomEnableForUnits(buf *bytes.Buffer, units map[string]dalec.SystemdUnitConfig) error {
	if len(units) == 0 {
		return nil
	}

	grouped := groupUnitsByBaseName(units)
	sorted := dalec.SortMapKeys(grouped)
	for _, v := range sorted {
		ls := grouped[v]
		if !requiresCustomEnable(ls) {
			continue
		}

		sortedNames := dalec.SortMapKeys(ls)
		for _, name := range sortedNames {
			cfg := ls[name]
			if err := writeCustomEnablePartial(buf, name, &cfg); err != nil {
				return errors.Wrapf(err, "error writing custom systemd enable template for unit: %s", name)
			}
			if err := customStartTmpl.Execute(buf, name); err != nil {
				return errors.Wrapf(err, "error writing custom systemd start template for unit: %s", name)
			}
		}
	}

	return nil
}

func writeCustomEnablePartial(buf io.Writer, name string, cfg *dalec.SystemdUnitConfig) error {
	if cfg.Enable {
		return customEnableTmpl.Execute(buf, name)
	}
	return customNoEnableTmpl.Execute(buf, name)
}

// requiresCustomEnable returns true when there is a mix of enabled and not
// enabled units.
//
// This expects to have a list of units that share a common
// basename. It does not check base names in any way.
// You can group by base name using [groupUnitsByBaseName].
func requiresCustomEnable(ls map[string]dalec.SystemdUnitConfig) bool {
	var enable int
	for _, v := range ls {
		if v.Enable {
			enable++
		}
	}

	if enable == 0 {
		return false
	}

	return enable != len(ls)
}
