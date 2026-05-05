package specgen

type Analysis struct {
	Facts               *RepoFacts       `json:"facts,omitempty"`
	Metadata            MetadataHint     `json:"metadata"`
	Languages           []string         `json:"languages,omitempty"`
	BuildDrivers        []string         `json:"build_drivers,omitempty"`
	PackageHints        []string         `json:"package_hints,omitempty"`
	RepoShape           RepoShape        `json:"repo_shape"`
	Runtime             RuntimeHint      `json:"runtime"`
	Artifacts           []ArtifactHint   `json:"artifacts,omitempty"`
	SelectedStrategy    string           `json:"selected_strategy"`
	SelectedRuntimeKind string           `json:"selected_runtime_kind"`
	Confidence          ConfidenceReport `json:"confidence"`
	Unresolved          []UnresolvedItem `json:"unresolved,omitempty"`
	Evidence            []Evidence       `json:"evidence,omitempty"`

	InstallLayout InstallLayout    `json:"install_layout,omitempty"`
	Decisions     []DecisionRecord `json:"decisions,omitempty"`
	Alternatives  *Alternatives    `json:"alternatives,omitempty"`

	// Transitional / legacy fields kept for compatibility with the rest of the
	// current tree until we update analysis.go / plan_deterministic.go / build.go.
	CandidateComponents []string      `json:"candidate_components,omitempty"`
	Services            []ServiceHint `json:"services,omitempty"`
	ManpagePaths        []string      `json:"manpage_paths,omitempty"`
	ConfigPaths         []string      `json:"config_paths,omitempty"`
	CICommands          []string      `json:"ci_commands,omitempty"`
	InstallHints        []string      `json:"install_hints,omitempty"`

	BuildHints    []CommandHint    `json:"build_hints,omitempty"`
	TestHints     []CommandHint    `json:"test_hints,omitempty"`
	InstallHints2 []CommandHint    `json:"install_hints_v2,omitempty"`
	DocHints      []CommandHint    `json:"doc_hints,omitempty"`
	Components    []ComponentHint  `json:"components,omitempty"`
	MakeTargets   []MakeTargetHint `json:"make_targets,omitempty"`
}

type MetadataHint struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Website     string `json:"website,omitempty"`
	License     string `json:"license"`
	Version     string `json:"version"`
}

type RepoShape struct {
	IsMonorepo       bool     `json:"is_monorepo"`
	HasWorkspace     bool     `json:"has_workspace"`
	HasMultipleBins  bool     `json:"has_multiple_bins"`
	HasMultipleApps  bool     `json:"has_multiple_apps"`
	PrimarySubdir    string   `json:"primary_subdir,omitempty"`
	CandidateSubdirs []string `json:"candidate_subdirs,omitempty"`
}

type RuntimeHint struct {
	Kind       string `json:"kind"` // cli|daemon|library|unknown
	Entrypoint string `json:"entrypoint,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	Confidence string `json:"confidence"` // high|medium|low
}

type ArtifactHint struct {
	Kind       string `json:"kind"` // binary|docs|wheel|data|manpage|config|libexec|unknown
	Name       string `json:"name,omitempty"`
	Path       string `json:"path,omitempty"`
	Confidence string `json:"confidence"` // high|medium|low
	Reason     string `json:"reason,omitempty"`
}

type ConfidenceReport struct {
	Metadata string `json:"metadata"` // high|medium|low
	Strategy string `json:"strategy"` // high|medium|low
	Runtime  string `json:"runtime"`  // high|medium|low
	Overall  string `json:"overall"`  // high|medium|low
}

type Evidence struct {
	Kind       string `json:"kind"`   // manifest|ci|buildfile|readme|heuristic|file-path
	Source     string `json:"source"` // filename / detector
	Detail     string `json:"detail"`
	Confidence string `json:"confidence,omitempty"` // high|medium|low
}

type UnresolvedItem struct {
	Code        string   `json:"code"`
	Message     string   `json:"message"`
	Severity    string   `json:"severity"` // high|medium|low
	Suggestions []string `json:"suggestions,omitempty"`
}

type ServiceHint struct {
	Name       string `json:"name,omitempty"`
	UnitPath   string `json:"unit_path,omitempty"`
	ConfigPath string `json:"config_path,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type CommandHint struct {
	Command    string `json:"command"`
	Source     string `json:"source,omitempty"`     // Makefile, workflow file, etc.
	Kind       string `json:"kind,omitempty"`       // build|test|install|doc|release
	Confidence string `json:"confidence,omitempty"` // high|medium|low
	Reason     string `json:"reason,omitempty"`
}

type ComponentHint struct {
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	Language   string `json:"language,omitempty"`
	Role       string `json:"role,omitempty"`       // cli|daemon|library|tool
	Confidence string `json:"confidence,omitempty"` // high|medium|low
	Reason     string `json:"reason,omitempty"`
}

type MakeTargetHint struct {
	Name       string   `json:"name"`
	Commands   []string `json:"commands,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

type DecisionRecord struct {
	Field      string   `json:"field"`
	Chosen     string   `json:"chosen"`
	Confidence string   `json:"confidence,omitempty"` // high|medium|low
	Reason     string   `json:"reason,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type ScoredChoice struct {
	Value      string   `json:"value"`
	Score      int      `json:"score"`
	Confidence string   `json:"confidence,omitempty"` // high|medium|low
	Reason     string   `json:"reason,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
}

type Alternatives struct {
	Components   []ScoredChoice `json:"components,omitempty"`
	BuildStyles  []ScoredChoice `json:"build_styles,omitempty"`
	EntryPoints  []ScoredChoice `json:"entrypoints,omitempty"`
	PackageNames []ScoredChoice `json:"package_names,omitempty"`
}

type InstallLayout struct {
	Binaries    []PathEvidence `json:"binaries,omitempty"`
	Manpages    []PathEvidence `json:"manpages,omitempty"`
	ConfigFiles []PathEvidence `json:"config_files,omitempty"`
	Docs        []PathEvidence `json:"docs,omitempty"`
	DataDirs    []PathEvidence `json:"data_dirs,omitempty"`
	Libexec     []PathEvidence `json:"libexec,omitempty"`
	Systemd     []PathEvidence `json:"systemd,omitempty"`
}

type PathEvidence struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`                 // binary|manpage|config|doc|data_dir|libexec|systemd
	Source     string `json:"source,omitempty"`     // detector / file / manifest / ci / makefile
	Confidence string `json:"confidence,omitempty"` // high|medium|low
	Reason     string `json:"reason,omitempty"`
}
