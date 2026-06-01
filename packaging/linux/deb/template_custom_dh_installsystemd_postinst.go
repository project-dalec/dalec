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

// This is used to generate a postinst (or at least part of a postinst) for the
// case where we have a mix of enabled/disabled units with the same basename.
// For all units that need this, the `dh_installsystemd` command should be
// executed with the `--no-enable` option.
// This handles enabled or not enabled for this special case instead of using
// the postinst provided by `dh_installsystemd` without `--no-eable` set.
func customDHInstallSystemdPostinst(spec *dalec.Spec, target string) ([]byte, error) {
	artifacts := spec.GetArtifacts(target)
	units := artifacts.Systemd.GetUnits()
	grouped := groupUnitsByBaseName(units)

	buf := bytes.NewBuffer(nil)

	sorted := dalec.SortMapKeys(grouped)
	for _, v := range sorted {
		ls := grouped[v]
		if !requiresCustomEnable(ls) {
			continue
		}

		sorted := dalec.SortMapKeys(ls)

		for _, name := range sorted {
			cfg := ls[name]
			if err := writeCustomEnablePartial(buf, name, &cfg); err != nil {
				return nil, errors.Wrapf(err, "error writing custom systemd enable template for unit: %s", name)
			}
			if err := customStartTmpl.Execute(buf, name); err != nil {
				return nil, errors.Wrapf(err, "error writing custom systemd start template for unit: %s", name)
			}
		}
	}

	return buf.Bytes(), nil
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
