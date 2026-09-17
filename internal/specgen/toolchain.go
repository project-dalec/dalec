package specgen

import (
	"fmt"
	"strconv"
	"strings"
)

// GoToolchainRequirement describes what Go toolchain a repo needs and how to
// satisfy it on a given target.
type GoToolchainRequirement struct {
	// MinVersion is the minimum Go version required, e.g. "1.22", "1.25.0".
	// Parsed from the go directive in go.mod.
	MinVersion string

	// MajorMinor is the normalised "X.Y" form used for package name lookups.
	// e.g. "1.25.0" → "1.25", "1.22" → "1.22".
	MajorMinor string

	// Major is the integer major version (always 1 for current Go).
	Major int
	// Minor is the integer minor version, e.g. 25 for Go 1.25.
	Minor int

	// NeedsNewer is true when the required version exceeds what the standard
	// distro package manager provides on common targets (jammy, azlinux3, noble).
	// When true the caller should emit a target-specific golang package override.
	NeedsNewer bool
}

// ParseGoToolchainRequirement parses a go.mod version string (e.g. "1.25",
// "1.25.0", "1.25.0") and returns a populated GoToolchainRequirement.
// Returns a zero-value struct if the version is empty or unparseable.
func ParseGoToolchainRequirement(goDirective string) GoToolchainRequirement {
	v := strings.TrimSpace(goDirective)
	if v == "" {
		return GoToolchainRequirement{}
	}

	// Normalise: strip patch component for major.minor extraction.
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return GoToolchainRequirement{MinVersion: v}
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return GoToolchainRequirement{MinVersion: v}
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return GoToolchainRequirement{MinVersion: v}
	}

	majorMinor := fmt.Sprintf("%d.%d", major, minor)

	// Version thresholds for common targets (conservative — use the lowest
	// known-good version each distro ships):
	//   azlinux3 / CBL-Mariner 3: golang ≈ 1.22
	//   jammy    / Ubuntu 22.04:  golang-go ≈ 1.18
	//   noble    / Ubuntu 24.04:  golang ≈ 1.22
	//
	// If the repo needs anything above 1.22 we flag it as NeedsNewer so the
	// plan can emit target-specific overrides.
	const maxStandardMinor = 22
	needsNewer := major > 1 || (major == 1 && minor > maxStandardMinor)

	return GoToolchainRequirement{
		MinVersion: v,
		MajorMinor: majorMinor,
		Major:      major,
		Minor:      minor,
		NeedsNewer: needsNewer,
	}
}

// GoToolchainDepForTarget returns the package name and optional version
// constraint string for the Go toolchain on a given dalec target.
//
// dalec PackageConstraints version strings use the same syntax as the
// underlying package manager:
//   - RPM targets (azlinux3): ">= 1.22.0"
//   - DEB targets (jammy, noble): ">= 1.22~"
//
// When the required version exceeds what the standard distro provides, the
// Microsoft Go package (msft-golang) is preferred on azlinux3 because it
// ships a current toolchain. On deb targets we fall back to recommending the
// golang-1.X-go naming convention used by Ubuntu PPA / deadsnakes style.
func GoToolchainDepForTarget(req GoToolchainRequirement, targetName string) (pkgName string, versionConstraint string) {
	if req.MajorMinor == "" {
		// No version information — emit bare "golang" and let the distro decide.
		return "golang", ""
	}

	low := strings.ToLower(strings.TrimSpace(targetName))

	switch {
	// ── azlinux3 / Mariner / CBL ──────────────────────────────────────────────
	case strings.Contains(low, "azlinux") || strings.Contains(low, "mariner") || strings.Contains(low, "cbl"):
		if req.NeedsNewer {
			// msft-golang ships a current Go toolchain on Mariner/AzureLinux.
			// It installs as /usr/local/go and sets PATH — compatible with dalec.
			return "msft-golang", ""
		}
		// Standard golang package with a floor version constraint.
		return "golang", fmt.Sprintf(">= %s.0", req.MajorMinor)

	// ── Ubuntu noble (24.04) ──────────────────────────────────────────────────
	case strings.Contains(low, "noble"):
		if req.NeedsNewer {
			// golang-go on noble is 1.22. For anything higher use the golang-X.Y-go
			// naming convention available via Ubuntu toolchain PPA, or flag it.
			return fmt.Sprintf("golang-%s-go", req.MajorMinor), ""
		}
		return "golang", fmt.Sprintf(">= %s~", req.MajorMinor)

	// ── Ubuntu jammy (22.04) ──────────────────────────────────────────────────
	case strings.Contains(low, "jammy"):
		if req.NeedsNewer {
			// jammy ships Go 1.18. Anything higher needs golang-X.Y-go from the
			// Ubuntu toolchain PPA. Emit the versioned package name so the spec
			// is reviewable; the user must ensure the PPA is configured.
			return fmt.Sprintf("golang-%s-go", req.MajorMinor), ""
		}
		return "golang", fmt.Sprintf(">= %s~", req.MajorMinor)

	// ── Windows cross ─────────────────────────────────────────────────────────
	case strings.Contains(low, "windows"):
		if req.NeedsNewer {
			return "msft-golang", ""
		}
		return "golang", fmt.Sprintf(">= %s.0", req.MajorMinor)

	// ── Unknown / generic fallback ────────────────────────────────────────────
	default:
		if req.NeedsNewer {
			// Can't know the right package name. Emit the generic "golang" with a
			// version floor and add an unresolved item (handled by the caller).
			return "golang", fmt.Sprintf(">= %s", req.MajorMinor)
		}
		return "golang", fmt.Sprintf(">= %s", req.MajorMinor)
	}
}

// IsGoVersionAtLeast returns true when version v is >= major.minor.
// v is in the form "1.25", "1.25.0", etc.
func IsGoVersionAtLeast(v string, major, minor int) bool {
	req := ParseGoToolchainRequirement(v)
	if req.MajorMinor == "" {
		return false
	}
	if req.Major != major {
		return req.Major > major
	}
	return req.Minor >= minor
}
