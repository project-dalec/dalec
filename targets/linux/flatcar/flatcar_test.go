package flatcar

import (
	"reflect"
	"testing"

	"github.com/project-dalec/dalec"
)

func TestSysextEnvDefaults(t *testing.T) {
	got := DefaultConfig.SysextEnv(&dalec.Spec{Name: "go-md2man"}, TargetKey)
	want := map[string]string{
		"DALEC_SYSEXT_OS_ID":        "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL": "1.0",
		"DALEC_SYSEXT_IMAGE_NAME":   "go-md2man",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSysupdateSysextEnvDefaults(t *testing.T) {
	cfg := &sysupdateConfig{Config: DefaultConfig}
	got := cfg.SysextEnv(&dalec.Spec{
		Name:     "go-md2man",
		Version:  "2.0.7",
		Revision: "3",
	}, TargetKey)
	want := map[string]string{
		"DALEC_SYSEXT_OS_ID":           "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL":    "1.0",
		"DALEC_SYSEXT_IMAGE_VERSION":   "v2.0.7-3",
		"DALEC_SYSEXT_SHA256SUMS_NAME": "go-md2man",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env\n got: %#v\nwant: %#v", got, want)
	}
}

func TestSysupdateSysextEnvDefaultsRevision(t *testing.T) {
	cfg := &sysupdateConfig{Config: DefaultConfig}
	got := cfg.SysextEnv(&dalec.Spec{
		Name:    "go-md2man",
		Version: "2.0.7",
	}, TargetKey)

	if got["DALEC_SYSEXT_IMAGE_VERSION"] != "v2.0.7-1" {
		t.Fatalf("expected default revision in image version, got: %#v", got)
	}
}
