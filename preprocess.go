package dalec

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

//go:embed scripts/gomod-patch.sh
var gomodPatchScriptTmpl string

const (
	// Gomod preprocessing constants
	gomodPatchSourcePrefix = "__gomod_patch_"
	gomodPatchFilename     = "gomod.patch"
	gomodFilename          = "go.mod"
	gosumFilename          = "go.sum"
	defaultGitUsername     = "git"
)

// Preprocess performs preprocessing on the spec after loading.
// This includes generating patches for gomod edits and potentially other
// generator-based transformations in the future.
//
// Preprocessing generates LLB states for patches and registers them as context sources
// that can be retrieved later when sources are fetched.
func (s *Spec) Preprocess(client gwclient.Client, sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) error {
	if err := s.preprocessGomodEdits(sOpt, worker, opts...); err != nil {
		return errors.Wrap(err, "failed to preprocess gomod edits")
	}

	return nil
}

// preprocessGomodEdits generates patch LLB states for all gomod replace/require directives
// and registers them as context sources that can be retrieved later.
func (s *Spec) preprocessGomodEdits(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) error {
	gomodSources := s.gomodSources()
	if len(gomodSources) == 0 {
		return nil
	}

	// Get sources with base patches applied
	baseSources := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := gomodSources[name]
		return ok
	}, opts...)

	credHelper, err := sOpt.GitCredHelperOpt()
	if err != nil {
		return errors.Wrap(err, "failed to get git credential helper")
	}

	// Generate patch states for each source with gomod generators
	for sourceName, src := range gomodSources {
		baseState, ok := baseSources[sourceName]
		if !ok {
			continue
		}

		for _, gen := range src.Generate {
			if gen == nil || gen.Gomod == nil {
				continue
			}

			// Generate patch state (LLB state, not solved bytes)
			patchSt, err := s.generateGomodPatchStateForSource(sourceName, gen, baseState, worker, credHelper, opts...)
			if err != nil {
				return errors.Wrapf(err, "failed to generate gomod patch state for source %s", sourceName)
			}

			if patchSt == nil {
				// No changes needed
				continue
			}

			// Create internal LLB source with the patch state
			patchSourceName := fmt.Sprintf(gomodPatchSourcePrefix+"%s", sourceName)
			s.Sources[patchSourceName] = Source{
				LLB: newSourceLLB(*patchSt),
			}

			// Inject patch reference into spec.Patches
			// Use PatchSpec.Path to point to the patch file within the context
			if s.Patches == nil {
				s.Patches = make(map[string][]PatchSpec)
			}

			strip := 1
			s.Patches[sourceName] = append(s.Patches[sourceName], PatchSpec{
				Source: patchSourceName,
				Path:   gomodPatchFilename, // The patch file within the context
				Strip:  &strip,
			})
		}
	}

	return nil
}

// gomodEditCommand generates the "go mod edit" command string from replace/require directives
func gomodEditCommand(g *GeneratorGomod) (string, error) {
	if g == nil || g.Edits == nil {
		return "", nil
	}

	var args []string

	// Process replace directives
	for _, r := range g.Edits.Replace {
		arg, err := r.goModEditArg()
		if err != nil {
			return "", err
		}
		args = append(args, "-replace="+arg)
	}

	// Process require directives
	for _, r := range g.Edits.Require {
		arg, err := r.goModEditArg()
		if err != nil {
			return "", err
		}
		args = append(args, "-require="+arg)
	}

	if len(args) == 0 {
		return "", nil
	}

	return "go mod edit " + strings.Join(args, " "), nil
}

// moduleInfo holds information about a Go module to be processed
type moduleInfo struct {
	RelModulePath string
	ModuleDir     string
	GoModPath     string
	GoSumPath     string
	RelGoModPath  string
	RelGoSumPath  string
}

// scriptTemplateData holds data for the gomod patch script template
type scriptTemplateData struct {
	PatchPath     string
	EditCmd       string
	GitConfig     string
	GoPrivate     string
	GoInsecure    string
	GoModFilename string
	GoSumFilename string
	Modules       []moduleInfo
}

// buildGomodPatchScript generates the shell script that applies gomod edits and captures diffs
func buildGomodPatchScript(editCmd string, paths []string, gen *SourceGenerator, sourceName string, patchOutputDir string) (string, error) {
	const (
		workDir = "/work/src"
	)

	patchPath := filepath.Join(patchOutputDir, gomodPatchFilename)
	joinedWorkDir := filepath.Join(workDir, sourceName, gen.Subpath)

	// Build git config section
	gitConfig := &strings.Builder{}
	var goPrivate, goInsecure string

	sortedHosts := SortMapKeys(gen.Gomod.Auth)
	if len(sortedHosts) > 0 {
		goPrivateHosts := make([]string, 0, len(sortedHosts))
		for _, host := range sortedHosts {
			auth := gen.Gomod.Auth[host]
			gpHost, _, _ := strings.Cut(host, ":")
			goPrivateHosts = append(goPrivateHosts, gpHost)

			if sshConfig := auth.SSH; sshConfig != nil {
				username := defaultGitUsername
				if sshConfig.Username != "" {
					username = sshConfig.Username
				}
				fmt.Fprintf(gitConfig, "git config --global url.\"ssh://%[1]s@%[2]s/\".insteadOf https://%[3]s/\n", username, host, gpHost)
				continue
			}

			var kind string
			switch {
			case auth.Token != "":
				kind = "token"
			case auth.Header != "":
				kind = "header"
			default:
				kind = ""
			}

			if kind != "" {
				fmt.Fprintf(gitConfig, "git config --global credential.\"https://%[1]s.helper\" \"/usr/local/bin/frontend credential-helper --kind=%[2]s\"\n", host, kind)
			}
		}

		joined := strings.Join(goPrivateHosts, ",")
		goPrivate = fmt.Sprintf("%q", joined)
		goInsecure = fmt.Sprintf("%q", joined)
	}

	// Build module info for each path
	modules := make([]moduleInfo, 0, len(paths))
	for _, relPath := range paths {
		moduleDir := filepath.Clean(filepath.Join(joinedWorkDir, relPath))
		relModulePath := filepath.Clean(filepath.Join(gen.Subpath, relPath))
		if relModulePath == "." {
			relModulePath = ""
		}

		relGoModPath := filepath.ToSlash(filepath.Join(relModulePath, gomodFilename))
		relGoSumPath := filepath.ToSlash(filepath.Join(relModulePath, gosumFilename))

		goModPath := filepath.Join(moduleDir, gomodFilename)
		goSumPath := filepath.Join(moduleDir, gosumFilename)

		modules = append(modules, moduleInfo{
			RelModulePath: relModulePath,
			ModuleDir:     moduleDir,
			GoModPath:     goModPath,
			GoSumPath:     goSumPath,
			RelGoModPath:  relGoModPath,
			RelGoSumPath:  relGoSumPath,
		})
	}

	// Prepare template data
	data := scriptTemplateData{
		PatchPath:     patchPath,
		EditCmd:       editCmd,
		GitConfig:     gitConfig.String(),
		GoPrivate:     goPrivate,
		GoInsecure:    goInsecure,
		GoModFilename: gomodFilename,
		GoSumFilename: gosumFilename,
		Modules:       modules,
	}

	// Execute template
	tmpl, err := template.New("gomod-patch").Parse(gomodPatchScriptTmpl)
	if err != nil {
		return "", errors.Wrap(err, "failed to parse gomod patch script template")
	}

	script := &strings.Builder{}
	if err := tmpl.Execute(script, data); err != nil {
		return "", errors.Wrap(err, "failed to execute gomod patch script template")
	}

	return script.String(), nil
}

// generateGomodPatchStateForSource generates a single merged patch LLB state for all paths
// in a gomod generator by running go mod edit + tidy and capturing the diff.
// Returns the LLB state containing the patch file, or nil if no changes are needed.
func (s *Spec) generateGomodPatchStateForSource(sourceName string, gen *SourceGenerator, baseState llb.State, worker llb.State, credHelper llb.RunOption, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	editCmd, err := gomodEditCommand(gen.Gomod)
	if err != nil {
		return nil, err
	}

	if editCmd == "" {
		return nil, nil
	}

	paths := gen.Gomod.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	const (
		workDir   = "/work/src"
		proxyPath = "/go/pkg/mod" // Standard Go module cache path
	)

	// Create a temporary directory for patch generation
	patchOutputDir := "/tmp/patch-work"

	// Generate the shell script
	scriptContent, err := buildGomodPatchScript(editCmd, paths, gen, sourceName, patchOutputDir)
	if err != nil {
		return nil, err
	}

	// Create a state with the script file
	scriptState := llb.Scratch().File(
		llb.Mkfile("/gomod-patch.sh", 0755, []byte(scriptContent)),
		WithConstraints(opts...),
	)

	// Create a scratch state to capture the patch output
	patchOutput := llb.Scratch()

	runOpts := []llb.RunOption{
		llb.Args([]string{"/gomod-patch.sh"}),
		llb.AddMount("/gomod-patch.sh", scriptState, llb.SourcePath("/gomod-patch.sh")),
		llb.AddMount(workDir, baseState),
		llb.AddMount(proxyPath, llb.Scratch(), llb.AsPersistentCacheDir(GomodCacheKey, llb.CacheMountShared)),
		llb.AddMount(patchOutputDir, patchOutput), // Mount scratch state to capture patch file
		llb.AddEnv("GOPATH", "/go"),
		llb.AddEnv("TMP_GOMODCACHE", proxyPath),
		llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"),
		WithConstraints(opts...),
		ProgressGroup("Generate gomod patch for source: " + sourceName),
	}

	if credHelper != nil {
		runOpts = append(runOpts, credHelper)
	}
	if secretOpt := gen.withGomodSecretsAndSockets(); secretOpt != nil {
		runOpts = append(runOpts, secretOpt)
	}

	// Generate the LLB state that captures the patch output mount
	// The AddMount call returns the state of the patchOutput scratch.
	// Since we mounted at patchOutputDir and wrote to patchPath,
	// the file in the mount will be at gomodPatchFilename (path relative to mount point)
	patchMount := worker.Run(runOpts...).AddMount(patchOutputDir, patchOutput)

	// The patch system expects files to be at /{sourceName}/{path}
	// So we need to copy the patch file to that location
	patchSourceName := fmt.Sprintf(gomodPatchSourcePrefix+"%s", sourceName)
	finalPatchPath := filepath.Join("/", patchSourceName, gomodPatchFilename)

	patchSt := llb.Scratch().
		File(llb.Mkdir(filepath.Join("/", patchSourceName), 0755, llb.WithParents(true)), WithConstraints(opts...)).
		File(llb.Copy(patchMount, "/"+gomodPatchFilename, finalPatchPath), WithConstraints(opts...))

	return &patchSt, nil
}
