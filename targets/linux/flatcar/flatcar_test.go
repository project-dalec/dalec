package flatcar

import (
	"reflect"
	"testing"

	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/targets/linux/deb/distro"
	"github.com/project-dalec/dalec/targets/linux/deb/ubuntu"
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

func TestDefaultConfigUsesNobleWorker(t *testing.T) {
	base, ok := DefaultConfig.Base.(*distro.Config)
	if !ok {
		t.Fatalf("expected default Flatcar base to use a deb distro config, got %T", DefaultConfig.Base)
	}

	if base.ContextRef != ubuntu.NobleWorkerContextName || base.AptCachePrefix != ubuntu.NobleAptCachePrefix {
		t.Fatalf("expected default Flatcar base to be Noble")
	}

	if _, ok := DefaultConfig.Base.(workerHandler); !ok {
		t.Fatalf("expected default Flatcar base to provide a worker target")
	}
}
