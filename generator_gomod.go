package dalec

import (
	"bytes"
	"context"
	goerrors "errors"
	"fmt"
	"path/filepath"
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
		script := g.gitconfigGeneratorScript(gomodDownloadWrapperBasename, opts...)
		scriptPath := filepath.Join(scriptMountpoint, gomodDownloadWrapperBasename)

		for _, path := range paths {
			in = worker.Run(
				// First download the go module deps to our persistent cache
				// Then set the GOPROXY to the cache dir so that we can extract just the deps we need
				// This allows us to persist the module cache across builds and avoid downloading
				// the same deps over and over again.
				ShArgs(`set -e; GOMODCACHE="${TMP_GOMODCACHE}" `+scriptPath+`; GOPROXY="file://${TMP_GOMODCACHE}/cache/download" `+scriptPath),
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

func (g *SourceGenerator) gitconfigGeneratorScript(scriptPath string, opts ...llb.ConstraintsOpt) llb.State {
	var script bytes.Buffer

	sortedHosts := SortMapKeys(g.Gomod.Auth)
	if len(sortedHosts) > 0 {
		fmt.Fprintln(&script, `set -eu`)
		script.WriteRune('\n')
	}

	goPrivate := []string{}

	for _, host := range sortedHosts {
		auth := g.Gomod.Auth[host]
		gpHost, _, _ := strings.Cut(host, ":")
		goPrivate = append(goPrivate, gpHost)

		script.WriteRune('\n')
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

	fmt.Fprintf(&script, "go env -w GOPRIVATE=%s", strings.Join(goPrivate, ","))
	script.WriteRune('\n')
	fmt.Fprintln(&script, "[ -f go.mod ]; go mod download")
	return llb.Scratch().File(llb.Mkfile(scriptPath, 0o755, script.Bytes()), opts...)
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

// GomodDeps returns an [llb.State] containing all the go module dependencies for the spec
// for any sources that have a gomod generator specified.
// If there are no sources with a gomod generator, this will return a nil state.
func (s *Spec) GomodDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) *llb.State {
	sources := s.gomodSources()
	if len(sources) == 0 {
		return nil
	}

	deps := llb.Scratch()

	// Get the patched sources for the go modules
	// This is needed in case a patch includes changes to go.mod or go.sum
	patched := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)

	sorted := SortMapKeys(patched)

	credHelperRunOpt, err := sOpt.GitCredHelperOpt()
	if err != nil {
		st := ErrorState(llb.Scratch(), errors.Wrap(err, "failed to get git credential helper"))
		return &st
	}

	for _, key := range sorted {
		src := s.Sources[key]

		opts := append(opts, ProgressGroup("Fetch go module dependencies for source: "+key))
		deps = deps.With(func(in llb.State) llb.State {
			for _, gen := range src.Generate {
				if gen.Gomod != nil {
					in = in.With(withGomod(gen, patched[key], worker, key, credHelperRunOpt, opts...))
				}
			}
			return in
		})
	}

	return &deps
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
