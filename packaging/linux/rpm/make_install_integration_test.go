package rpm

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"gotest.tools/v3/assert"
)

func TestMakeInstallIntegration(t *testing.T) {
	// Create a spec that uses make install pattern
	spec := &dalec.Spec{
		Name:        "make-install-example",
		Description: "Example project using make install to place artifacts",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "MIT",
		Artifacts: dalec.Artifacts{
			PackageFiles: map[string]string{
				"rpm": `%{_bindir}/myapp
%{_mandir}/man1/myapp.1*`,
			},
		},
	}

	// Generate the RPM spec and verify it contains our custom files
	var buf bytes.Buffer
	err := WriteSpec(spec, "test", &buf)
	assert.NilError(t, err, "Failed to generate RPM spec")

	specContent := buf.String()

	// Verify the %files section contains our custom listings
	assert.Check(t, strings.Contains(specContent, "%files"), "Generated spec should contain %files section")
	assert.Check(t, strings.Contains(specContent, "%{_bindir}/myapp"), "Generated spec should contain myapp binary")
	assert.Check(t, strings.Contains(specContent, "%{_mandir}/man1/myapp.1*"), "Generated spec should contain myapp manpage")

	// Verify the %install section is minimal (since make install handles installation)
	assert.Check(t, strings.Contains(specContent, "%install"), "Generated spec should contain %install section")

	// Verify we don't have the old artifact copy commands since we're using custom files
	assert.Check(t, !strings.Contains(specContent, "mkdir -p %{buildroot}/%{_bindir}"), 
		"Should not contain traditional artifact copy commands when using custom files")

	t.Logf("Generated RPM spec:\n%s", specContent)
}