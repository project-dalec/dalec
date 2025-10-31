package dalec

import (
	"bytes"
	"context"
	goerrors "errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

const (
	gomodCacheDir = "/go/pkg/mod"
	// GoModCacheKey is the key used to identify the go module cache in the buildkit cache.
	// It is exported only for testing purposes.
	GomodCacheKey = "dalec-gomod-proxy-cache"
)

const (
	// GomodPatchArchiveName is the directory extracted from the archive containing gomod patches.
	GomodPatchArchiveName = "__internal_dalec_gomod_generator_deps"
	// GomodPatchArchiveFilename is the tarball added to source packages that stores gomod patches.
	// This ensures the patches are included in SRPMs/debian.tar.xz for build reproducibility.
	GomodPatchArchiveFilename = GomodPatchArchiveName + ".tar.gz"
)

// GomodPatch represents an auto-generated patch that captures go.mod/go.sum
// edits requested via GeneratorGomod directives. The patch will be applied to
// the associated source during packaging and is shipped inside the source
// artifact (e.g. SRPM) to preserve provenance.
type GomodPatch struct {
	// SourceName is the spec source that this patch should be applied to.
	SourceName string
	// FileName is the patch filename that will be emitted into the SOURCES directory.
	FileName string
	// Strip is the -p value to pass to patch.
	Strip int
	// State contains the patch contents as an llb.State rooted at FileName.
	State llb.State
	// Contents stores the raw patch bytes, when available.
	Contents []byte
}

// ArchivePath returns the relative path to this patch inside the gomod patch archive.
func (p *GomodPatch) ArchivePath() string {
	if p == nil {
		return ""
	}
	return path.Join(GomodPatchArchiveName, p.SourceName, p.FileName)
}

// EnsureGomodPatches generates gomod patches for all sources with replace/require directives
// if they haven't been generated yet. This is called early in the build process to ensure
// patches are available for packaging.
func (s *Spec) EnsureGomodPatches(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) error {
	if !s.HasGomods() {
		return nil
	}

	if s.gomodPatchesGenerated {
		return nil
	}

	patches, err := s.generateGomodPatches(sOpt, worker, opts...)
	if err != nil {
		return err
	}

	s.gomodPatches = patches
	s.gomodPatchesGenerated = true
	return nil
}

// GomodPatchArchive returns a state containing all generated go.mod patches packaged into
// a tarball named [GomodPatchArchiveFilename]. The archive has the layout
// `GomodPatchArchiveName/<sourceName>/<patchFile>`.
func (s *Spec) GomodPatchArchive(worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	patches := s.GomodPatches()
	if len(patches) == 0 {
		return nil, nil
	}

	states := make([]llb.State, 0, len(patches))
	for _, patch := range patches {
		destPath := "/" + patch.ArchivePath()
		st := llb.Scratch().File(llb.Copy(patch.State, patch.FileName, destPath, WithCreateDestPath()))
		states = append(states, st)
	}

	merged := MergeAtPath(llb.Scratch(), states, "/", opts...)
	tarred := merged.With(AsTar(worker, GomodPatchArchiveFilename, opts...))
	return &tarred, nil
}

// GomodPatchesForSource returns all go.mod patches that should be applied to the
// provided source name. The list is sorted to ensure deterministic ordering.
func (s *Spec) GomodPatchesForSource(name string) []*GomodPatch {
	if len(s.gomodPatches) == 0 {
		return nil
	}

	patches := s.gomodPatches[name]
	if len(patches) == 0 {
		return nil
	}

	out := make([]*GomodPatch, len(patches))
	copy(out, patches)
	sort.Slice(out, func(i, j int) bool {
		if out[i].FileName == out[j].FileName {
			return out[i].SourceName < out[j].SourceName
		}
		return out[i].FileName < out[j].FileName
	})
	return out
}

// GomodPatches returns all generated go.mod patches sorted by SourceName and
// FileName.
func (s *Spec) GomodPatches() []*GomodPatch {
	if len(s.gomodPatches) == 0 {
		return nil
	}

	var result []*GomodPatch
	for _, list := range s.gomodPatches {
		result = append(result, list...)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].SourceName == result[j].SourceName {
			return result[i].FileName < result[j].FileName
		}
		return result[i].SourceName < result[j].SourceName
	})
	return result
}

// GomodPatchSources returns the sorted list of sources that have generated
// go.mod patches.
func (s *Spec) GomodPatchSources() []string {
	if len(s.gomodPatches) == 0 {
		return nil
	}

	keys := make([]string, 0, len(s.gomodPatches))
	for k := range s.gomodPatches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// AddGomodPatchForTesting allows tests to inject a gomod patch without running the
// full generator pipeline.
func (s *Spec) AddGomodPatchForTesting(patch *GomodPatch) {
	if patch == nil {
		return
	}
	s.registerGomodPatch(patch)
	if err := s.appendGomodPatchExtensionEntry(patch); err != nil {
		panic(err)
	}
}

func (s *Spec) generateGomodPatches(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (map[string][]*GomodPatch, error) {
	gomodSources := s.gomodSources()
	if len(gomodSources) == 0 {
		return map[string][]*GomodPatch{}, nil
	}

	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := gomodSources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources for gomod patches")
	}

	credHelper, err := sOpt.GitCredHelperOpt()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get git credential helper for gomod patches")
	}

	patches := make(map[string][]*GomodPatch)

	for sourceName, src := range gomodSources {
		base, ok := patched[sourceName]
		if !ok {
			continue
		}

		for idx, gen := range src.Generate {
			if gen == nil || gen.Gomod == nil {
				continue
			}

			genPatches, err := buildGomodPatchesForGenerator(sourceName, idx, gen, base, worker, credHelper, opts...)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to generate gomod patch for source %s generator %d", sourceName, idx)
			}

			if len(genPatches) == 0 {
				continue
			}

			patches[sourceName] = append(patches[sourceName], genPatches...)
		}
	}

	return patches, nil
}

// buildGomodPatchesForGenerator creates patches for a single generator's gomod directives.
// It processes all paths specified in the generator configuration.
func buildGomodPatchesForGenerator(sourceName string, generatorIndex int, gen *SourceGenerator, base llb.State, worker llb.State, credHelper llb.RunOption, opts ...llb.ConstraintsOpt) ([]*GomodPatch, error) {
	commands, err := gomodEditCommandLines(gen.Gomod)
	if err != nil {
		return nil, err
	}

	paths := gen.Gomod.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}

	patches := make([]*GomodPatch, 0, len(paths))
	for pathIndex, relPath := range paths {
		patch, err := generateGomodPatchForPath(sourceName, generatorIndex, pathIndex, relPath, gen, base, worker, credHelper, commands, opts...)
		if err != nil {
			return nil, err
		}
		if patch != nil {
			patches = append(patches, patch)
		}
	}

	return patches, nil
}

// generateGomodPatchForPath generates a patch for a single go module path by:
// 1. Capturing the original go.mod/go.sum
// 2. Applying replace/require directives and running go mod tidy
// 3. Diffing the changes to create a unified patch
func generateGomodPatchForPath(sourceName string, generatorIndex, pathIndex int, relPath string, gen *SourceGenerator, base llb.State, worker llb.State, credHelper llb.RunOption, commands []string, opts ...llb.ConstraintsOpt) (*GomodPatch, error) {
	const (
		workDir         = "/work/src"
		patchMountpoint = "/tmp/dalec/internal/gomod/patch-output"
		proxyPath       = "/tmp/dalec/gomod-proxy-cache"
	)

	joinedWorkDir := filepath.Join(workDir, sourceName, gen.Subpath)
	moduleDir := filepath.Clean(filepath.Join(joinedWorkDir, relPath))

	relModulePath := filepath.Clean(filepath.Join(gen.Subpath, relPath))
	if relModulePath == "." {
		relModulePath = ""
	}

	relGoModPath := filepath.ToSlash(filepath.Join(relModulePath, "go.mod"))
	relGoSumPath := filepath.ToSlash(filepath.Join(relModulePath, "go.sum"))

	baseName := fmt.Sprintf("%s-gomod-%d-%d", sanitizeForFilename(sourceName), generatorIndex, pathIndex)
	if relModulePath != "" {
		baseName = fmt.Sprintf("%s-%s", baseName, sanitizeForFilename(relModulePath))
	}
	patchFileName := baseName + ".patch"

	moduleDirQuoted := fmt.Sprintf("%q", moduleDir)
	goModPath := filepath.Join(moduleDir, "go.mod")
	goSumPath := filepath.Join(moduleDir, "go.sum")
	patchFilePath := filepath.Join(patchMountpoint, patchFileName)

	script := &strings.Builder{}
	script.WriteString("set -e\n")
	script.WriteString("export GOMODCACHE=\"${TMP_GOMODCACHE}\"\n")
	script.WriteString("patch_file=" + fmt.Sprintf("%q", patchFilePath) + "\n")
	script.WriteString("if [ ! -f " + fmt.Sprintf("%q", goModPath) + " ]; then\n  : > \"$patch_file\"\n  exit 0\nfi\n")
	script.WriteString("tmpdir=$(mktemp -d)\n")
	script.WriteString("cp " + fmt.Sprintf("%q", goModPath) + " \"$tmpdir/go.mod\"\n")
	script.WriteString("had_go_sum=0\n")
	script.WriteString("if [ -f " + fmt.Sprintf("%q", goSumPath) + " ]; then cp " + fmt.Sprintf("%q", goSumPath) + " \"$tmpdir/go.sum\"; had_go_sum=1; else : > \"$tmpdir/go.sum\"; fi\n")
	script.WriteString("(\n")
	script.WriteString("  cd " + moduleDirQuoted + "\n")

	sortedHosts := SortMapKeys(gen.Gomod.Auth)
	if len(sortedHosts) > 0 {
		goPrivateHosts := make([]string, 0, len(sortedHosts))
		for _, host := range sortedHosts {
			auth := gen.Gomod.Auth[host]
			gpHost, _, _ := strings.Cut(host, ":")
			goPrivateHosts = append(goPrivateHosts, gpHost)

			if sshConfig := auth.SSH; sshConfig != nil {
				username := "git"
				if sshConfig.Username != "" {
					username = sshConfig.Username
				}
				fmt.Fprintf(script, "  git config --global url.\"ssh://%[1]s@%[2]s/\".insteadOf https://%[3]s/\n", username, host, gpHost)
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
				fmt.Fprintf(script, "  git config --global credential.\"https://%[1]s.helper\" \"/usr/local/bin/frontend credential-helper --kind=%[2]s\"\n", host, kind)
			}
		}

		joined := strings.Join(goPrivateHosts, ",")
		fmt.Fprintf(script, "  export GOPRIVATE=%q\n", joined)
		fmt.Fprintf(script, "  export GOINSECURE=%q\n", joined)
	}
	for _, cmd := range commands {
		script.WriteString("  " + cmd + "\n")
	}
	// Tidy to ensure go.mod and go.sum are consistent
	script.WriteString("  go mod tidy\n")
	// Download any new dependencies that tidy added
	script.WriteString("  go mod download\n")
	script.WriteString(")\n")
	script.WriteString("if [ ! -f " + fmt.Sprintf("%q", goSumPath) + " ]; then touch " + fmt.Sprintf("%q", goSumPath) + "; fi\n")
	script.WriteString("mkdir -p $(dirname \"$patch_file\")\n")
	script.WriteString(": > \"$patch_file\"\n")
	script.WriteString("changed=0\n")
	fmt.Fprintf(script, `if diff -u --label a/%[1]s --label b/%[1]s "$tmpdir/go.mod" %[2]q >> "$patch_file"; then
	true
else
	status=$?
	if [ "$status" -gt 1 ]; then exit "$status"; fi
	changed=1
fi
`, relGoModPath, goModPath)
	script.WriteString("if [ -f " + fmt.Sprintf("%q", goSumPath) + " ] || [ -s \"$tmpdir/go.sum\" ]; then\n")
	fmt.Fprintf(script, `  if diff -u --label a/%[1]s --label b/%[1]s "$tmpdir/go.sum" %[2]q >> "$patch_file"; then
		true
	else
		status=$?
		if [ "$status" -gt 1 ]; then exit "$status"; fi
		changed=1
	fi
fi
`, relGoSumPath, goSumPath)
	script.WriteString("cp \"$tmpdir/go.mod\" " + fmt.Sprintf("%q", goModPath) + "\n")
	script.WriteString("if [ \"$had_go_sum\" -eq 1 ]; then\n  cp \"$tmpdir/go.sum\" " + fmt.Sprintf("%q", goSumPath) + "\nelse\n  rm -f " + fmt.Sprintf("%q", goSumPath) + "\nfi\n")
	script.WriteString("if [ \"$changed\" -eq 0 ]; then\n  : > \"$patch_file\"\nfi\n")
	script.WriteString("rm -rf \"$tmpdir\"\n")

	runOpts := []llb.RunOption{
		ShArgs(script.String()),
		llb.AddMount(workDir, base),
		llb.AddMount(patchMountpoint, llb.Scratch()),
		llb.AddMount(proxyPath, llb.Scratch(), llb.AsPersistentCacheDir(GomodCacheKey, llb.CacheMountShared)),
		llb.AddEnv("GOPATH", "/go"),
		llb.AddEnv("TMP_GOMODCACHE", proxyPath),
		llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"),
		WithConstraints(opts...),
	}

	if credHelper != nil {
		runOpts = append(runOpts, credHelper)
	}
	if secretOpt := gen.withGomodSecretsAndSockets(); secretOpt != nil {
		runOpts = append(runOpts, secretOpt)
	}

	run := worker.Run(runOpts...)

	patchOutput := run.AddMount(patchMountpoint, llb.Scratch())
	patchState := llb.Scratch().File(llb.Copy(patchOutput, patchFileName, patchFileName))

	return &GomodPatch{
		SourceName: sourceName,
		FileName:   patchFileName,
		Strip:      1,
		State:      patchState,
	}, nil
}

func gomodEditCommandLines(g *GeneratorGomod) ([]string, error) {
	var cmds []string
	for _, replace := range g.GetReplace() {
		arg, err := replace.goModEditArg()
		if err != nil {
			return nil, errors.Wrap(err, "invalid gomod replace configuration")
		}
		cmds = append(cmds, fmt.Sprintf("go mod edit -replace=%q", arg))
	}

	for _, require := range g.GetRequire() {
		arg, err := require.goModEditArg()
		if err != nil {
			return nil, errors.Wrap(err, "invalid gomod require configuration")
		}
		cmds = append(cmds, fmt.Sprintf("go mod edit -require=%q", arg))
	}

	return cmds, nil
}

// sanitizeForFilename converts a string into a safe filename by replacing
// non-alphanumeric characters with underscores.
func sanitizeForFilename(s string) string {
	if s == "" || s == "." {
		return "root"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == '/', r == '\\':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	res := b.String()
	if res == "" {
		return "root"
	}
	return res
}

func (g *GeneratorGomod) processBuildArgs(args map[string]string, allowArg func(key string) bool) error {
	var errs []error
	lex := shell.NewLex('\\')
	// force the shell lexer to skip unresolved env vars so they aren't
	// replaced with ""
	lex.SkipUnsetEnv = true

	for host, auth := range g.Auth {
		subbed, err := expandArgs(lex, host, args, allowArg)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		g.Auth[subbed] = auth
		if subbed != host {
			delete(g.Auth, host)
		}
	}

	if g.Edits != nil {
		for i := range g.Edits.Replace {
			updatedOld, err := expandArgs(lex, g.Edits.Replace[i].Old, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "gomod replace[%d]", i))
				continue
			}

			updatedNew, err := expandArgs(lex, g.Edits.Replace[i].New, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "gomod replace[%d]", i))
				continue
			}

			updatedOld = strings.TrimSpace(updatedOld)
			updatedNew = strings.TrimSpace(updatedNew)
			if updatedOld == "" || updatedNew == "" {
				errs = append(errs, errors.Errorf("gomod replace[%d] resolved to an empty value", i))
				continue
			}

			g.Edits.Replace[i] = GomodReplace{Old: updatedOld, New: updatedNew}
		}

		for i := range g.Edits.Require {
			updatedModule, err := expandArgs(lex, g.Edits.Require[i].Module, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "gomod require[%d]", i))
				continue
			}

			updatedVersion, err := expandArgs(lex, g.Edits.Require[i].Version, args, allowArg)
			if err != nil {
				errs = append(errs, errors.Wrapf(err, "gomod require[%d]", i))
				continue
			}

			updatedModule = strings.TrimSpace(updatedModule)
			updatedVersion = strings.TrimSpace(updatedVersion)
			if updatedModule == "" || updatedVersion == "" {
				errs = append(errs, errors.Wrapf(err, "gomod require[%d] resolved to an empty value", i))
				continue
			}

			g.Edits.Require[i] = GomodRequire{Module: updatedModule, Version: updatedVersion}
		}
	}

	return goerrors.Join(errs...)
}

func (s *Source) isGomod() bool {
	for _, gen := range s.Generate {
		if gen.Gomod != nil {
			return true
		}
	}
	return false
}

// HasGomods returns true if any of the sources in the spec are a go module.
func (s *Spec) HasGomods() bool {
	for _, src := range s.Sources {
		if src.isGomod() {
			return true
		}
	}
	return false
}

func withGomod(g *SourceGenerator, srcSt, worker llb.State, subPath string, credHelper llb.RunOption, opts ...llb.ConstraintsOpt) func(llb.State) llb.State {
	return func(in llb.State) llb.State {
		const (
			workDir                      = "/work/src"
			scriptMountpoint             = "/tmp/dalec/internal/gomod"
			gomodDownloadWrapperBasename = "go_mod_download.sh"
		)

		joinedWorkDir := filepath.Join(workDir, subPath, g.Subpath)
		srcMount := llb.AddMount(workDir, srcSt)

		paths := g.Gomod.Paths
		if g.Gomod.Paths == nil {
			paths = []string{"."}
		}

		const proxyPath = "/tmp/dalec/gomod-proxy-cache"

		// Pass in git auth if necessary
		script := g.gitconfigGeneratorScript(gomodDownloadWrapperBasename)
		scriptPath := filepath.Join(scriptMountpoint, gomodDownloadWrapperBasename)

		for _, path := range paths {
			command := fmt.Sprintf(`set -e
	if [ -f go.mod ]; then
	  GOMODCACHE="${TMP_GOMODCACHE}" %[1]s
	fi
	mkdir -p %[2]s
	cp -a "${TMP_GOMODCACHE}/." %[2]s/ || true
	if [ -f go.mod ]; then
	  GOMODCACHE="%[2]s" GOPROXY="file://${TMP_GOMODCACHE}/cache/download" %[1]s
	fi
	`, scriptPath, gomodCacheDir)

			in = worker.Run(
				// First download the go module deps to our persistent cache
				// Then mirror that cache into the build output so downstream builds can stay offline.
				// Finally, re-run the download using the mirrored cache as the GOPROXY to ensure the
				// extracted module tree and download cache are complete under /go/pkg/mod.
				ShArgs(command),
				g.withGomodSecretsAndSockets(),
				llb.AddMount(scriptMountpoint, script),
				llb.AddEnv("GOPATH", "/go"),
				credHelper,
				llb.AddEnv("TMP_GOMODCACHE", proxyPath),
				llb.AddEnv("GIT_SSH_COMMAND", "ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no"),
				llb.Dir(filepath.Join(joinedWorkDir, path)),
				srcMount,
				llb.AddMount(proxyPath, llb.Scratch(), llb.AsPersistentCacheDir(GomodCacheKey, llb.CacheMountShared)),
				WithConstraints(opts...),
				g.Gomod._sourceMap.GetLocation(in),
			).AddMount(gomodCacheDir, in)
		}

		return in
	}
}

func (g *SourceGenerator) gitconfigGeneratorScript(scriptPath string) llb.State {
	var script bytes.Buffer

	fmt.Fprintln(&script, "set -eu")
	fmt.Fprintln(&script)
	fmt.Fprintln(&script, "if [ ! -f go.mod ]; then")
	fmt.Fprintln(&script, "  exit 0")
	fmt.Fprintln(&script, "fi")

	sortedHosts := SortMapKeys(g.Gomod.Auth)
	goPrivate := []string{}

	if len(sortedHosts) > 0 {
		script.WriteRune('\n')
	}

	for _, host := range sortedHosts {
		auth := g.Gomod.Auth[host]
		gpHost, _, _ := strings.Cut(host, ":")
		goPrivate = append(goPrivate, gpHost)

		if sshConfig := auth.SSH; sshConfig != nil {
			username := "git"
			if sshConfig.Username != "" {
				username = sshConfig.Username
			}

			// By default, go will make a request to git for the source of a
			// package, and it will specify the remote url as https://<package
			// name>. Because SSH auth was requested for this host, tell git to
			// use ssh for upstreams with this host name.
			fmt.Fprintf(&script, `git config --global url."ssh://%[1]s@%[2]s/".insteadOf https://%[3]s/`, username, host, gpHost)
			script.WriteRune('\n')
			continue
		}

		var kind string
		switch {
		case auth.Token != "":
			kind = "token"
		case auth.Header != "":
			kind = "header"
		default:
			continue
		}

		fmt.Fprintf(&script, `git config --global credential."https://%[1]s.helper" "/usr/local/bin/frontend credential-helper --kind=%[2]s"`, host, kind)
		script.WriteRune('\n')
	}

	if len(goPrivate) > 0 {
		script.WriteRune('\n')
		joined := strings.Join(goPrivate, ",")
		fmt.Fprintf(&script, "go env -w GOPRIVATE=%s\n", joined)
		fmt.Fprintf(&script, "go env -w GOINSECURE=%s\n", joined)
	}

	if len(g.Gomod.GetReplace()) > 0 {
		script.WriteRune('\n')
		fmt.Fprintln(&script, "if [ -f go.mod ]; then")
		for _, replace := range g.Gomod.GetReplace() {
			// Note: validation happens during spec loading via validateGomodDirectives()
			// so this should never error in practice. The validation ensures all directives
			// are well-formed before we reach this point.
			arg, _ := replace.goModEditArg()
			fmt.Fprintf(&script, "  go mod edit -replace=%q\n", arg)
		}
		fmt.Fprintln(&script, "fi")
	}

	if len(g.Gomod.GetRequire()) > 0 {
		script.WriteRune('\n')
		fmt.Fprintln(&script, "if [ -f go.mod ]; then")
		for _, require := range g.Gomod.GetRequire() {
			// Note: validation happens during spec loading via validateGomodDirectives()
			// so this should never error in practice. The validation ensures all directives
			// are well-formed before we reach this point.
			arg, _ := require.goModEditArg()
			fmt.Fprintf(&script, "  go mod edit -require=%q\n", arg)
		}
		fmt.Fprintln(&script, "fi")
	}

	script.WriteRune('\n')
	fmt.Fprintln(&script, "go mod download")
	fmt.Fprintln(&script, "go mod tidy")
	fmt.Fprintln(&script, "go mod download")
	return llb.Scratch().File(llb.Mkfile(scriptPath, 0o755, script.Bytes()))
}

func (g *SourceGenerator) withGomodSecretsAndSockets() llb.RunOption {
	return runOptionFunc(func(ei *llb.ExecInfo) {
		if g.Gomod == nil {
			return
		}

		const basePath = "/run/secrets"

		for host, auth := range g.Gomod.Auth {
			if auth.Token != "" {
				p := filepath.Join(basePath, host, "token")
				llb.AddSecret(p, llb.SecretID(auth.Token)).SetRunOption(ei)
				continue
			}

			if auth.Header != "" {
				p := filepath.Join(basePath, host, "header")
				llb.AddSecret(p, llb.SecretID(auth.Header)).SetRunOption(ei)
				continue
			}

			if auth.SSH != nil {
				llb.AddSSHSocket(llb.SSHID(auth.SSH.ID)).SetRunOption(ei)

				llb.AddEnv(
					"GIT_SSH_COMMAND",
					`ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no`,
				).SetRunOption(ei)
			}
		}
	})
}

func (s *Spec) gomodSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isGomod() {
			sources[name] = src
		}
	}
	return sources
}

// sourceHasGomodDirectives returns true if the source has any gomod replace or require directives.
// that would modify the go.mod file and potentially change dependencies.
func (s *Source) sourceHasGomodDirectives() bool {
	for _, gen := range s.Generate {
		if gen != nil && gen.Gomod != nil && gen.Gomod.HasEdits() {
			return true
		}
	}
	return false
}

// validateGomodDirectives validates that all gomod replace and require directives
// are well-formed. This should be called during spec loading to catch errors early.
func (g *GeneratorGomod) validateGomodDirectives() error {
	if g == nil {
		return nil
	}

	for i, replace := range g.GetReplace() {
		if _, err := replace.goModEditArg(); err != nil {
			return fmt.Errorf("invalid gomod replace[%d]: %w", i, err)
		}
	}

	for i, require := range g.GetRequire() {
		if _, err := require.goModEditArg(); err != nil {
			return fmt.Errorf("invalid gomod require[%d]: %w", i, err)
		}
	}

	return nil
}

// validateGomodDirectives validates all gomod directives in the spec.
// This is called during spec loading to catch configuration errors early,
// before they would cause panics during build execution.
func (s *Spec) validateGomodDirectives() error {
	for sourceName, src := range s.Sources {
		for genIdx, gen := range src.Generate {
			if gen != nil && gen.Gomod != nil {
				if err := gen.Gomod.validateGomodDirectives(); err != nil {
					return fmt.Errorf("source %q generator[%d]: %w", sourceName, genIdx, err)
				}
			}
		}
	}
	return nil
}

// GomodDeps returns an [llb.State] containing all the go module dependencies for the spec
// for any sources that have a gomod generator specified.
// If there are no sources with a gomod generator, this will return a nil state.
func (s *Spec) GomodDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) (*llb.State, error) {
	sources := s.gomodSources()
	if len(sources) == 0 {
		return nil, nil
	}

	if err := s.EnsureGomodPatches(sOpt, worker, opts...); err != nil {
		return nil, errors.Wrap(err, "failed to prepare gomod patches")
	}

	// Capture the original (pre-patch) source states so we can pre-populate the
	// shared gomod cache with modules referenced by the upstream go.sum before
	// our gomod edits/tidy steps potentially prune them away.
	//
	// Performance optimization: Only fetch base states for sources that have
	// replace/require directives. Sources without these directives don't need
	// the dual-download approach since their dependencies won't change.
	baseSourceStates := map[string]llb.State{}
	sourcesNeedingBaseline := []string{}
	for name, src := range sources {
		if src.sourceHasGomodDirectives() {
			sourcesNeedingBaseline = append(sourcesNeedingBaseline, name)
		}
	}

	if len(sourcesNeedingBaseline) > 0 {
		if allSources, err := Sources(s, sOpt, opts...); err == nil {
			for _, name := range sourcesNeedingBaseline {
				if st, ok := allSources[name]; ok {
					baseSourceStates[name] = st
				}
			}
		} else {
			return nil, errors.Wrap(err, "failed to prepare gomod sources")
		}
	}

	deps := llb.Scratch()

	// Get the patched sources for the go modules
	// This is needed in case a patch includes changes to go.mod or go.sum
	patched, err := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get patched sources")
	}

	sorted := SortMapKeys(patched)

	credHelperRunOpt, err := sOpt.GitCredHelperOpt()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get git credential helper")
	}

	for _, key := range sorted {
		src := s.Sources[key]

		baseState, hasBase := baseSourceStates[key]
		patchedState := patched[key]

		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				if gen.Gomod == nil {
					continue
				}
				// First run against the unpatched tree to mirror any dependencies
				// that exist prior to our go.mod/go.sum modifications.
				// This is only needed when replace/require directives are present.
				if hasBase {
					pgOpts := append(opts, ProgressGroup("Fetch baseline go module dependencies for source: "+key))
					in = in.With(withGomod(gen, baseState, worker, key, credHelperRunOpt, pgOpts...))
				}
				// Then run with the patched sources (or just once if no replace/require).
				// The cache reflects the final module graph after any go mod edits.
				progressMsg := "Fetch go module dependencies for source: " + key
				if hasBase {
					progressMsg = "Fetch updated go module dependencies for source: " + key
				}
				pgOpts := append(opts, ProgressGroup(progressMsg))
				in = in.With(withGomod(gen, patchedState, worker, key, credHelperRunOpt, pgOpts...))
			}
			return in
		})
	}

	return &deps, nil
}

func (gen *GeneratorGomod) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal GeneratorGomod
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode gomod generator")
	}

	*gen = GeneratorGomod(i)
	gen._sourceMap = newSourceMap(ctx, node)
	return nil
}
