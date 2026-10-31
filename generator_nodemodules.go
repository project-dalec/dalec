package dalec

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml/ast"
	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

const npmProxyConfigScript = `
configure_npm_proxy() {
	restore_xtrace=0
	case "$-" in
		*x*) set +x; restore_xtrace=1 ;;
	esac
	restore_npm_xtrace() {
		if [ "${restore_xtrace}" = "1" ]; then
			set -x
		fi
	}

	if [ "${DALEC_DISABLE_PROXY_CONFIG:-}" = "1" ]; then
		restore_npm_xtrace
		return 0
	fi

	http_proxy_value="${HTTP_PROXY:-${http_proxy:-}}"
	https_proxy_value="${HTTPS_PROXY:-${https_proxy:-}}"
	no_proxy_value="${NO_PROXY:-${no_proxy:-}}"
	if [ -z "${http_proxy_value}" ] && [ -z "${https_proxy_value}" ]; then
		restore_npm_xtrace
		return 0
	fi

	if [ -n "${http_proxy_value}" ]; then
		export npm_config_proxy="${http_proxy_value}"
	fi
	if [ -n "${https_proxy_value}" ]; then
		export npm_config_https_proxy="${https_proxy_value}"
	fi
	if [ -n "${no_proxy_value}" ]; then
		export npm_config_noproxy="${no_proxy_value}"
	fi

	for ca_bundle in \
		/etc/ssl/certs/ca-certificates.crt \
		/etc/pki/tls/certs/ca-bundle.crt \
		/etc/ssl/ca-bundle.pem \
		/etc/pki/tls/cacert.pem \
		/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
		/etc/ssl/cert.pem
	do
		if [ -f "${ca_bundle}" ]; then
			export NODE_EXTRA_CA_CERTS="${ca_bundle}"
			export npm_config_cafile="${ca_bundle}"
			break
		fi
	done

	restore_npm_xtrace
}
`

func (s *Source) isNodeMod() bool {
	for _, gen := range s.Generate {
		if gen.NodeMod != nil {
			return true
		}
	}
	return false
}

// HasNodeMods returns true if any of the sources in the spec are node modules.
func (s *Spec) HasNodeMods() bool {
	for _, src := range s.Sources {
		if src.isNodeMod() {
			return true
		}
	}
	return false
}

func nodeProxyConfig(sOpt SourceOpts) llb.RunOption {
	return RunOptFunc(func(ei *llb.ExecInfo) {
		if sOpt.DisableProxyConfig() {
			llb.AddEnv(BuildArgDalecDisableProxyConfig, "1").SetRunOption(ei)
		}
	})
}

// nodeModules runs `npm install` for each configured path of the generator
// against the provided source state and returns a state containing only the
// resulting node_modules directories, nested under the source name.
func nodeModules(g *SourceGenerator, sOpt SourceOpts, worker llb.State, name string, in llb.State, opts ...llb.ConstraintsOpt) llb.State {
	workDir := "/work/src"
	joinedWorkDir := filepath.Join(workDir, name, g.Subpath)
	const installBasePath = "/work/download"

	paths := g.NodeMod.Paths
	if g.NodeMod.Paths == nil {
		paths = []string{"."}
	}

	states := make([]llb.State, 0, len(paths))
	for _, path := range paths {
		// npm installs into the directory passed to --prefix and reads the
		// package.json from there, so the install runs against a writable copy
		// of the source. Only the node_modules directory is taken from the
		// result so that nothing else npm may have touched (or anything the
		// source was patched with) leaks into the returned state.
		installRel := filepath.Join(name, g.Subpath, path)
		installPath := filepath.Join(installBasePath, installRel)
		// The proxy setup must run in a shell so its environment changes reach
		// npm. Quote user-controlled values before embedding them in that shell
		// command so registry URLs cannot be interpreted as shell syntax.
		installCmd := npmProxyConfigScript + "\nconfigure_npm_proxy\nnpm install --prefix " + shellQuote(installPath)
		if g.NodeMod.Registry != "" {
			installCmd += " --registry=" + shellQuote(g.NodeMod.Registry)
		}

		st := worker.Run(
			ShArgs(installCmd),
			nodeProxyConfig(sOpt),
			llb.Dir(filepath.Join(joinedWorkDir, path)),
			WithConstraints(opts...),
			llb.AddMount(workDir, in, llb.Readonly),
			llb.IgnoreCache,
			g.NodeMod._sourceMap.GetLocation(in),
		).AddMount(installBasePath, in)

		// Use an include pattern rather than copying node_modules directly
		// since npm does not create the directory when there are no
		// dependencies to install.
		modules := llb.Scratch().File(
			llb.Copy(st, installRel, installRel, WithIncludes([]string{"node_modules"}), WithDirContentsOnly(), WithCreateDestPath()),
			opts...,
		)
		states = append(states, modules)
	}
	return MergeAtPath(llb.Scratch(), states, "/", opts...)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (s *Spec) nodeModSources() map[string]Source {
	sources := map[string]Source{}
	for name, src := range s.Sources {
		if src.isNodeMod() {
			sources[name] = src
		}
	}
	return sources
}

// NodeModDeps returns a map[string]llb.State containing all the node module dependencies for the spec
// for any sources that have a node module generator specified.
// If there are no sources with a node module generator, this will return nil.
// The returned states have node_modules installed for each relevant source, using sources as input.
// The dependencies are installed from the patched sources, but the spec patches
// are not applied to the returned states themselves so that callers can apply
// them in the same way as for any other source.
func (s *Spec) NodeModDeps(sOpt SourceOpts, worker llb.State, opts ...llb.ConstraintsOpt) map[string]llb.State {
	sources := s.nodeModSources()
	if len(sources) == 0 {
		return nil
	}

	// npm must see the patched sources (e.g. a patched package.json), but the
	// returned states replace the unpatched sources that targets apply the
	// spec patches to. Only the installed node_modules are layered on top of
	// the unpatched source so that the patches are not applied twice.
	patched := s.getPatchedSources(sOpt, worker, func(name string) bool {
		_, ok := sources[name]
		return ok
	}, opts...)

	unpatched := make(map[string]llb.State, len(sources))
	for name, src := range sources {
		unpatched[name] = src.ToState(name, sOpt, opts...)
	}

	result := make(map[string]llb.State)
	sorted := SortMapKeys(sources)
	opts = append(opts, ProgressGroup("Fetch node module dependencies for sources"))
	for _, key := range sorted {
		src := sources[key]
		states := []llb.State{unpatched[key]}
		for _, gen := range src.Generate {
			if gen.NodeMod == nil {
				continue
			}
			states = append(states, nodeModules(gen, sOpt, worker, key, patched[key], opts...))
		}
		merged := MergeAtPath(llb.Scratch(), states, "/", opts...)
		result[key] = merged.With(sourceFilterAtPath(sOpt, key, opts...))
	}
	return result
}

func (gen *GeneratorNodeMod) UnmarshalYAML(ctx context.Context, node ast.Node) error {
	type internal GeneratorNodeMod
	var i internal

	dec := getDecoder(ctx)
	if err := dec.DecodeFromNodeContext(ctx, node, &i); err != nil {
		return errors.Wrap(err, "failed to decode node module generator")
	}

	*gen = GeneratorNodeMod(i)
	gen._sourceMap = newSourceMap(ctx, node)
	return nil
}
