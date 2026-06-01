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
