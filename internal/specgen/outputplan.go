package specgen

import "strings"

type OutputProfile string

const (
	OutputProfileAuto             OutputProfile = "auto"
	OutputProfilePackage          OutputProfile = "package"
	OutputProfilePackageContainer OutputProfile = "package+container"
	OutputProfileContainer        OutputProfile = "container"
	OutputProfileSysext           OutputProfile = "sysext"
	OutputProfileWindowsCross     OutputProfile = "windowscross"
)

type IntentMode string

const (
	IntentAuto             IntentMode = "auto"
	IntentPackage          IntentMode = "package"
	IntentPackageContainer IntentMode = "package+container"
	IntentContainerOnly    IntentMode = "container-only"
	IntentSysext           IntentMode = "sysext"
	IntentWindowsCross     IntentMode = "windowscross"
)

type TargetFamily string

const (
	TargetFamilyAuto    TargetFamily = "auto"
	TargetFamilyRPM     TargetFamily = "rpm"
	TargetFamilyDEB     TargetFamily = "deb"
	TargetFamilyBoth    TargetFamily = "both"
	TargetFamilyWindows TargetFamily = "windows"
)

type TestMode string

const (
	TestAuto   TestMode = "auto"
	TestAlways TestMode = "always"
	TestNever  TestMode = "never"
)

type TargetRoute struct {
	Name       string `json:"name"`
	Subtarget  string `json:"subtarget,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Confidence string `json:"confidence,omitempty"` // high|medium|low
}

type PlannedArtifact struct {
	Kind        string
	Path        string
	Subpath     string
	Name        string
	Target      string // actual Dalec target scoping only
	BuildTarget string // language-specific source build target, e.g. ./cmd/operator
	Required    bool
	Confidence  string
	Reason      string
	Mode        uint32
	User        string
	Group       string
}

type PlannedDependency struct {
	Name            string            `json:"name"`
	Scope           string            `json:"scope"` // build|runtime|test|recommends|sysext
	Target          string            `json:"target,omitempty"`
	Version         string            `json:"version,omitempty"`
	Constraints     map[string]string `json:"constraints,omitempty"`
	PackageManagers []string          `json:"package_managers,omitempty"`
	Confidence      string            `json:"confidence,omitempty"` // high|medium|low
	Reason          string            `json:"reason,omitempty"`
}

type PlannedTest struct {
	Name       string            `json:"name"`
	Dir        string            `json:"dir,omitempty"`
	Steps      []string          `json:"steps,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
	Target     string            `json:"target,omitempty"`
	Confidence string            `json:"confidence,omitempty"` // high|medium|low
	Reason     string            `json:"reason,omitempty"`
}

type UserIntentPlan struct {
	RequestedIntent        IntentMode   `json:"requested_intent,omitempty"`
	RequestedTargetFamily  TargetFamily `json:"requested_target_family,omitempty"`
	RequestedMainComponent string       `json:"requested_main_component,omitempty"`
	RequestedBinaryNames   []string     `json:"requested_binary_names,omitempty"`
	RequestedPackageName   string       `json:"requested_package_name,omitempty"`
	RequestedBinaryName    string       `json:"requested_binary_name,omitempty"`
	RequestedBuildStyle    string       `json:"requested_build_style,omitempty"`
	RequestedBuildTarget   string       `json:"requested_build_target,omitempty"`
	RequestedEntrypoint    string       `json:"requested_entrypoint,omitempty"`
	RequestedCmd           string       `json:"requested_cmd,omitempty"`
	RequestedTestMode      TestMode     `json:"requested_test_mode,omitempty"`
	ExplicitFields         []string     `json:"explicit_fields,omitempty"`
}

type SpecPlan struct {
	SchemaVersion int           `json:"schema_version"`
	OutputProfile OutputProfile `json:"output_profile,omitempty"`
	Intent        IntentMode    `json:"intent"`
	TargetFamily  TargetFamily  `json:"target_family"`

	Routes []TargetRoute `json:"routes,omitempty"`

	MainComponent      string `json:"main_component,omitempty"`
	PackageName        string `json:"package_name,omitempty"`
	PrimaryBinaryName  string `json:"primary_binary_name,omitempty"`
	PrimaryBuildTarget string `json:"primary_build_target,omitempty"`
	Description        string `json:"description,omitempty"`
	License            string `json:"license,omitempty"`
	Website            string `json:"website,omitempty"`

	BuildStyle string `json:"build_style,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
	Cmd        string `json:"cmd,omitempty"`

	// NetworkMode controls the build network policy.
	// "none" for reproducible Go/Rust builds with vendored deps.
	// "" or "sandbox" for builds that need outbound access.
	NetworkMode string `json:"network_mode,omitempty"`

	// LDFlagsVarPath is the fully-qualified Go variable path for -ldflags version
	// injection, e.g. "github.com/foo/bar/cmd.Version" or "main.version".
	// Only meaningful for Go builds.
	LDFlagsVarPath string `json:"ldflags_var_path,omitempty"`

	// CGOEnabled controls whether CGO_ENABLED=1 is set for Go builds.
	// nil means auto (defaults to disabled for most baseline Go builds).
	CGOEnabled *bool `json:"cgo_enabled,omitempty"`

	Args map[string]string `json:"args,omitempty"`

	UseTargets    bool `json:"use_targets,omitempty"`
	GenerateTests bool `json:"generate_tests,omitempty"`

	Artifacts    []PlannedArtifact   `json:"artifacts,omitempty"`
	Dependencies []PlannedDependency `json:"dependencies,omitempty"`
	Tests        []PlannedTest       `json:"tests,omitempty"`

	Decisions    []DecisionRecord `json:"decisions,omitempty"`
	Alternatives *Alternatives    `json:"alternatives,omitempty"`
	UserIntent   *UserIntentPlan  `json:"user_intent,omitempty"`

	Warnings   []string         `json:"warnings,omitempty"`
	Unresolved []UnresolvedItem `json:"unresolved,omitempty"`
}

func ParseOutputProfile(s string) OutputProfile {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case string(OutputProfilePackage):
		return OutputProfilePackage
	case string(OutputProfilePackageContainer):
		return OutputProfilePackageContainer
	case string(OutputProfileContainer):
		return OutputProfileContainer
	case string(OutputProfileSysext):
		return OutputProfileSysext
	case string(OutputProfileWindowsCross):
		return OutputProfileWindowsCross
	default:
		return OutputProfileAuto
	}
}

func IntentFromOutputProfile(p OutputProfile) IntentMode {
	switch p {
	case OutputProfilePackage:
		return IntentPackage
	case OutputProfilePackageContainer:
		return IntentPackageContainer
	case OutputProfileContainer:
		return IntentContainerOnly
	case OutputProfileSysext:
		return IntentSysext
	case OutputProfileWindowsCross:
		return IntentWindowsCross
	default:
		return IntentAuto
	}
}

func ParseIntentMode(s string) IntentMode {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case string(IntentPackage):
		return IntentPackage
	case string(IntentPackageContainer):
		return IntentPackageContainer
	case string(IntentContainerOnly):
		return IntentContainerOnly
	case string(IntentSysext):
		return IntentSysext
	case string(IntentWindowsCross):
		return IntentWindowsCross
	default:
		return IntentAuto
	}
}

func ParseTargetFamily(s string) TargetFamily {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case string(TargetFamilyRPM):
		return TargetFamilyRPM
	case string(TargetFamilyDEB):
		return TargetFamilyDEB
	case string(TargetFamilyBoth):
		return TargetFamilyBoth
	case string(TargetFamilyWindows):
		return TargetFamilyWindows
	default:
		return TargetFamilyAuto
	}
}

func ParseTestMode(s string) TestMode {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case string(TestAlways):
		return TestAlways
	case string(TestNever):
		return TestNever
	default:
		return TestAuto
	}
}
