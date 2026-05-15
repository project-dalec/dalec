package linux

import (
	"reflect"
	"testing"
)

func TestSysextEnvFromBuildArgs(t *testing.T) {
	in := map[string]string{
		"DALEC_SYSEXT_IMAGE_NAME":    "myext",
		"DALEC_SYSEXT_OS_ID":         "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL":  "1.0",
		"DALEC_SYSEXT_OS_VERSION_ID": "",
		"SOME_OTHER_ARG":             "x",
	}

	got := sysextEnvFromBuildArgs(in)
	want := map[string]string{
		"DALEC_SYSEXT_IMAGE_NAME":   "myext",
		"DALEC_SYSEXT_OS_ID":        "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL": "1.0",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env\n got: %#v\nwant: %#v", got, want)
	}
}

func TestMergeSysextEnv_DefaultsAndOverrides(t *testing.T) {
	defaults := map[string]string{
		"DALEC_SYSEXT_OS_ID":         "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL":  "1.0",
		"DALEC_SYSEXT_IMAGE_NAME":    "go-md2man",
		"DALEC_SYSEXT_OS_VERSION_ID": "",
	}

	buildArgs := map[string]string{
		"DALEC_SYSEXT_IMAGE_NAME": "myext", // override default
		"SOME_OTHER_ARG":          "x",     // ignored
	}

	got := mergeSysextEnv(defaults, buildArgs)
	want := map[string]string{
		"DALEC_SYSEXT_OS_ID":        "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL": "1.0",
		"DALEC_SYSEXT_IMAGE_NAME":   "myext",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected env\n got: %#v\nwant: %#v", got, want)
	}
}

func TestMergeSysextEnv_DropsDefaultSysextLevelWhenVersionPinned(t *testing.T) {
	defaults := map[string]string{
		"DALEC_SYSEXT_OS_ID":        "flatcar",
		"DALEC_SYSEXT_SYSEXT_LEVEL": "1.0",
	}

	t.Run("drops default level when OS_VERSION_ID is set and SYSEXT_LEVEL is not", func(t *testing.T) {
		buildArgs := map[string]string{
			"DALEC_SYSEXT_OS_VERSION_ID": "4593.0.0",
		}

		got := mergeSysextEnv(defaults, buildArgs)

		if _, ok := got["DALEC_SYSEXT_SYSEXT_LEVEL"]; ok {
			t.Fatalf("expected SYSEXT_LEVEL to be dropped, got: %#v", got)
		}
		if got["DALEC_SYSEXT_OS_ID"] != "flatcar" {
			t.Fatalf("expected OS_ID=flatcar, got: %#v", got)
		}
		if got["DALEC_SYSEXT_OS_VERSION_ID"] != "4593.0.0" {
			t.Fatalf("expected OS_VERSION_ID=4593.0.0, got: %#v", got)
		}
	})

	t.Run("keeps explicitly provided level", func(t *testing.T) {
		buildArgs := map[string]string{
			"DALEC_SYSEXT_OS_VERSION_ID": "4593.0.0",
			"DALEC_SYSEXT_SYSEXT_LEVEL":  "2.0",
		}

		got := mergeSysextEnv(defaults, buildArgs)

		if got["DALEC_SYSEXT_SYSEXT_LEVEL"] != "2.0" {
			t.Fatalf("expected SYSEXT_LEVEL=2.0, got: %#v", got)
		}
		if got["DALEC_SYSEXT_OS_VERSION_ID"] != "4593.0.0" {
			t.Fatalf("expected OS_VERSION_ID=4593.0.0, got: %#v", got)
		}
	})
}
