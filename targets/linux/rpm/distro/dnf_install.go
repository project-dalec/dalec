package distro

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/project-dalec/dalec"
	"github.com/project-dalec/dalec/packaging/linux/rpm"
)

var dnfRepoPlatform = dalec.RepoPlatformConfig{
	ConfigRoot: "/etc/yum.repos.d",
	GPGKeyRoot: "/etc/pki/rpm-gpg",
	ConfigExt:  ".repo",
}

type PackageInstaller func(*dnfInstallConfig, string, []string) llb.RunOption

type dnfInstallConfig struct {
	// path for gpg keys to import for using a repo. These files for these keys
	// must also be added as mounts
	keys []string

	// Sets the root path to install rpms too.
	// this acts like installing to a chroot.
	root string

	// Additional mounts to add to the (t?)dnf install command (useful if installing RPMS which are mounted to a local directory)
	mounts []llb.RunOption

	constraints []llb.ConstraintsOpt

	// When true, don't omit docs from the installed RPMs.
	includeDocs bool

	forceArch string

	disableProxyConfig bool
}

type DnfInstallOpt func(*dnfInstallConfig)

// joinUnderRoot joins a rootfs path with an absolute container path.
// We must not use filepath.Join(root, "/abs") because that drops `root`.
func joinUnderRoot(root, abs string) string {
	if root == "" {
		return abs
	}
	return path.Join(root, strings.TrimPrefix(abs, "/"))
}

// see comment in tdnfInstall for why this additional option is needed
func DnfImportKeys(keys []string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.keys = append(cfg.keys, keys...)
	}
}

func DnfWithMounts(opts ...llb.RunOption) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.mounts = append(cfg.mounts, opts...)
	}
}

func DnfAtRoot(root string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.root = root
	}
}

func DnfForceArch(arch string) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.forceArch = arch
	}
}

func IncludeDocs(v bool) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.includeDocs = v
	}
}

func DnfWithSourceOpts(sOpt dalec.SourceOpts) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.disableProxyConfig = sOpt.DisableProxyConfig()
	}
}

func DnfInstallWithConstraints(opts []llb.ConstraintsOpt) DnfInstallOpt {
	return func(cfg *dnfInstallConfig) {
		cfg.constraints = append(cfg.constraints, opts...)
	}
}

func dnfInstallFlags(cfg *dnfInstallConfig) string {
	var cmdOpts string

	if cfg.root != "" {
		cmdOpts += " --installroot=" + cfg.root
		cmdOpts += " --setopt=reposdir=/etc/yum.repos.d"
	}

	if !cfg.includeDocs {
		cmdOpts += " --setopt=tsflags=nodocs"
	}

	return cmdOpts
}

func dnfInstallOptions(cfg *dnfInstallConfig, opts []DnfInstallOpt) {
	for _, o := range opts {
		o(cfg)
	}
}

func importGPGScript(keyPaths []string) string {
	// all keys that are included should be mounted under this path
	keyRoot := "/etc/pki/rpm-gpg"

	importScript := "#!/usr/bin/env sh\nset -eux\n"
	for _, keyPath := range keyPaths {
		keyName := filepath.Base(keyPath)
		fullPath := filepath.Join(keyRoot, keyName)
		// rpm --import requires armored keys, check if key is armored and convert if needed
		importScript += fmt.Sprintf(`
key_path="%s"
gpg --import --armor "${key_path}"

if ! head -1 "${key_path}" | grep -q 'BEGIN PGP PUBLIC KEY BLOCK'; then
	gpg --armor --export > /tmp/key.asc
	key_path="/tmp/key.asc"
fi
rpm --import "${key_path}"
`, fullPath)
	}

	return importScript
}

const dnfProxyConfigScript = `
cleanup_dnf_proxy() {
	if [ -n "${DALEC_DNF_PROXY_TRUST_BUNDLE_ACTIVE:-}" ]; then
		if [ -n "${DALEC_DNF_PROXY_TRUST_BUNDLE_BACKUP:-}" ] && [ -f "${DALEC_DNF_PROXY_TRUST_BUNDLE_BACKUP}" ]; then
			cp -p "${DALEC_DNF_PROXY_TRUST_BUNDLE_BACKUP}" "${DALEC_DNF_PROXY_TRUST_BUNDLE_ACTIVE}" 2>/dev/null || true
			rm -f "${DALEC_DNF_PROXY_TRUST_BUNDLE_BACKUP}" 2>/dev/null || true
		fi
		unset DALEC_DNF_PROXY_TRUST_BUNDLE_ACTIVE
		unset DALEC_DNF_PROXY_TRUST_BUNDLE_BACKUP
	fi
}

sync_dnf_proxy_trust_bundle() {
	source_bundle="${1}"
	trust_bundle="${DALEC_RPM_PROXY_TRUST_BUNDLE:-/etc/pki/tls/certs/ca-bundle.trust.crt}"
	if [ ! -f "${source_bundle}" ] || [ ! -f "${trust_bundle}" ]; then
		return 0
	fi
	if [ "${source_bundle}" = "${trust_bundle}" ]; then
		return 0
	fi
	if ! grep -q '# buildkit proxy CA begin' "${source_bundle}" 2>/dev/null; then
		return 0
	fi
	if grep -q '# buildkit proxy CA begin' "${trust_bundle}" 2>/dev/null; then
		return 0
	fi

	mkdir -p /tmp/dalec
	backup="${DALEC_RPM_PROXY_TRUST_BUNDLE_BACKUP:-/tmp/dalec/dnf-proxy-ca-bundle.trust.crt.bak}"
	cp -p "${trust_bundle}" "${backup}"
	sed -n '/# buildkit proxy CA begin/,/# buildkit proxy CA end/p' "${source_bundle}" >> "${trust_bundle}"
	DALEC_DNF_PROXY_TRUST_BUNDLE_ACTIVE="${trust_bundle}"
	DALEC_DNF_PROXY_TRUST_BUNDLE_BACKUP="${backup}"
}

configure_dnf_proxy() {
	restore_xtrace=0
	case "$-" in
		*x*) set +x; restore_xtrace=1 ;;
	esac

	if [ "${DALEC_DISABLE_PROXY_CONFIG:-}" = "1" ]; then
		if [ "${restore_xtrace}" = "1" ]; then
			set -x
		fi
		return 0
	fi

	http_proxy_value="${HTTP_PROXY:-${http_proxy:-}}"
	https_proxy_value="${HTTPS_PROXY:-${https_proxy:-}}"
	if [ -z "${http_proxy_value}" ] && [ -z "${https_proxy_value}" ]; then
		if [ "${restore_xtrace}" = "1" ]; then
			set -x
		fi
		return 0
	fi

	dnf_proxy_ca_bundle="${DALEC_RPM_PROXY_CA_BUNDLE:-}"
	if [ -n "${dnf_proxy_ca_bundle}" ] && [ ! -f "${dnf_proxy_ca_bundle}" ]; then
		dnf_proxy_ca_bundle=""
	fi
	if [ -z "${dnf_proxy_ca_bundle}" ]; then
		for ca_bundle in \
			/etc/ssl/certs/ca-certificates.crt \
			/etc/pki/tls/certs/ca-bundle.crt \
			/etc/ssl/ca-bundle.pem \
			/etc/pki/tls/cacert.pem \
			/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem \
			/etc/ssl/cert.pem
		do
			if [ -f "${ca_bundle}" ]; then
				dnf_proxy_ca_bundle="${ca_bundle}"
				break
			fi
		done
	fi

	if [ -n "${dnf_proxy_ca_bundle}" ]; then
		sync_dnf_proxy_trust_bundle "${dnf_proxy_ca_bundle}"
		install_flags="${install_flags} --setopt=sslverify=1 --setopt=sslcacert=${dnf_proxy_ca_bundle}"
		export SSL_CERT_FILE="${dnf_proxy_ca_bundle}"
		export CURL_CA_BUNDLE="${dnf_proxy_ca_bundle}"
	fi

	if [ "${restore_xtrace}" = "1" ]; then
		set -x
	fi
}
`

func dnfCommand(cfg *dnfInstallConfig, releaseVer string, exe string, dnfSubCmd []string, dnfArgs []string) llb.RunOption {
	const importKeysPath = "/tmp/dalec/internal/dnf/import-keys.sh"

	cacheDir := "/var/cache/" + exe
	if cfg.root != "" {
		cacheDir = joinUnderRoot(cfg.root, cacheDir)
	}
	installFlags := dnfInstallFlags(cfg)
	installFlags += " -y --setopt varsdir=/etc/dnf/vars --releasever=" + releaseVer + " "
	forceArch := cfg.forceArch
	installScriptDt := `#!/usr/bin/env bash
set -eux -o pipefail

` + dnfProxyConfigScript + `

import_keys_path="` + importKeysPath + `"
cmd="` + exe + `"
install_flags="` + installFlags + `"
force_arch="` + forceArch + `"
dnf_sub_cmd="` + strings.Join(dnfSubCmd, " ") + `"
cache_dir="` + cacheDir + `"

if [ -x "$import_keys_path" ]; then
	"$import_keys_path"
fi

if [ "$cmd" = "tdnf" ] && command -v dnf &>/dev/null; then
	# tdnf has a lot of limitations that cause issues (no --forcearch, issues with gpg keys on local file installs)
	# We already have dnf, so prefer that.
	cmd="dnf"
fi

if [ -n "$force_arch" ]; then
	if [ "$cmd" = "tdnf" ]; then
		echo "tdnf does not support --forcearch; cross-arch installs must use dnf" >&2
		exit 70
	fi
	install_flags="$install_flags --forcearch=$force_arch"
fi

configure_dnf_proxy
trap cleanup_dnf_proxy EXIT

$cmd $dnf_sub_cmd $install_flags "${@}"
`
	var runOpts []llb.RunOption

	installScript := llb.Scratch().File(llb.Mkfile("install.sh", 0o700, []byte(installScriptDt)), cfg.constraints...)
	const installScriptPath = "/tmp/dalec/internal/dnf/install.sh"

	runOpts = append(runOpts, llb.AddMount(installScriptPath, installScript, llb.SourcePath("install.sh"), llb.Readonly))

	// TODO(adamperlin): see if this can be removed for dnf
	// If we have keys to import in order to access a repo, we need to create a script to use `gpg` to import them
	// This is an unfortunate consequence of a bug in tdnf (see https://github.com/vmware/tdnf/issues/471).
	// The keys *should* be imported automatically by tdnf as long as the repo config references them correctly and
	// we mount the key files themselves under the right path. However, tdnf does NOT do this
	// currently if the keys are referenced via a `file:///` type url,
	// and we must manually import the keys as well.
	if len(cfg.keys) > 0 {
		importScript := importGPGScript(cfg.keys)
		runOpts = append(runOpts, llb.AddMount(importKeysPath,
			llb.Scratch().File(llb.Mkfile("/import-keys.sh", 0755, []byte(importScript)), cfg.constraints...),
			llb.Readonly,
			llb.SourcePath("/import-keys.sh")))
	}

	cmd := make([]string, 0, len(dnfArgs)+1)
	cmd = append(cmd, installScriptPath)
	cmd = append(cmd, dnfArgs...)

	runOpts = append(runOpts, llb.Args(cmd))
	runOpts = append(runOpts, cfg.mounts...)
	if cfg.disableProxyConfig {
		runOpts = append(runOpts, llb.AddEnv(dalec.BuildArgDalecDisableProxyConfig, "1"))
	}

	return dalec.WithRunOptions(runOpts...)
}

func (cfg *Config) InstallIntoRoot(rootfsPath string, pkgs []string, targetArch string, buildPlat ocispecs.Platform, sOpt dalec.SourceOpts) llb.RunOption {
	return dalec.RunOptFunc(func(ei *llb.ExecInfo) {
		// Ensure the package manager runs on the build/executor platform (native),
		// while installing into the mounted target rootfs via --installroot.
		bp := buildPlat
		ei.Constraints.Platform = &bp

		installOpts := []DnfInstallOpt{
			DnfAtRoot(rootfsPath),
			DnfForceArch(targetArch),
			DnfWithSourceOpts(sOpt),
			DnfInstallWithConstraints([]llb.ConstraintsOpt{dalec.WithConstraint(&ei.Constraints)}),
		}

		var installCfg dnfInstallConfig
		dnfInstallOptions(&installCfg, installOpts)

		cacheKey := cfg.CacheName
		if cfg.CacheAddPlatform {
			cacheKey += "-" + targetArch
		}
		// Cross-arch installs always use dnf --forcearch --installroot
		runOpts := []llb.RunOption{
			DnfInstall(&installCfg, cfg.ReleaseVer, pkgs),
		}

		// Mount package manager caches under the target rootfs (may include multiple dirs).
		for _, d := range cfg.CacheDir {
			if d == "" {
				continue
			}
			k := cacheKey
			if len(cfg.CacheDir) > 1 {
				k = cacheKey + "-" + filepath.Base(d)
			}
			runOpts = append(runOpts,
				llb.AddMount(
					joinUnderRoot(rootfsPath, d),
					llb.Scratch(),
					llb.AsPersistentCacheDir(k, llb.CacheMountLocked),
				),
			)
		}

		dalec.WithRunOptions(runOpts...).SetRunOption(ei)
	})
}

func DnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "dnf", append([]string{"install"}, pkgs...), nil)
}

// TdnfInstall uses tdnf to install packages
// NOTE: tdnf will be automatically upgraded to dnf to work around tdnf limitations *if* dnf is available
func TdnfInstall(cfg *dnfInstallConfig, releaseVer string, pkgs []string) llb.RunOption {
	return dnfCommand(cfg, releaseVer, "tdnf", append([]string{"install"}, pkgs...), nil)
}

func (cfg *Config) InstallBuildDeps(spec *dalec.Spec, sOpt dalec.SourceOpts, targetKey string, opts ...llb.ConstraintsOpt) llb.StateOption {
	deps := spec.GetPackageDeps(targetKey).GetBuild()
	if len(deps) == 0 {
		return dalec.NoopStateOption
	}

	repos := spec.GetBuildRepos(targetKey)
	return cfg.WithDeps(sOpt, targetKey, spec.Name, deps, repos, opts...)
}

func (cfg *Config) WithDeps(sOpt dalec.SourceOpts, targetKey, pkgName string, deps dalec.PackageDependencyList, repos []dalec.PackageRepositoryConfig, opts ...llb.ConstraintsOpt) llb.StateOption {
	return func(in llb.State) llb.State {
		if len(deps) == 0 {
			return in
		}

		opts = append(opts, dalec.ProgressGroup(fmt.Sprintf("Installing dependencies for %s", pkgName)))

		spec := &dalec.Spec{
			Name:        fmt.Sprintf("%s-dependencies", pkgName),
			Description: "Virtual Package to install dependencies for " + pkgName,
			Version:     "1.0",
			License:     "Apache 2.0",
			Revision:    "1",
			Dependencies: &dalec.PackageDependencies{
				Runtime: deps,
			},
		}

		rpmSpec := rpm.RPMSpec(spec, in, targetKey, "", opts...)

		specPath := filepath.Join("SPECS", spec.Name, spec.Name+".spec")
		cacheInfo := rpm.CacheInfo{TargetKey: targetKey, Caches: spec.Build.Caches}
		rpmDir := rpm.Build(rpmSpec, in, specPath, cacheInfo, opts...)

		const rpmMountDir = "/tmp/internal/dalec/deps/install/rpms"

		repoMounts, keyPaths := cfg.RepoMounts(repos, sOpt, opts...)

		installOpts := []DnfInstallOpt{
			DnfWithMounts(llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"), llb.Readonly)),
			DnfWithMounts(repoMounts),
			DnfImportKeys(keyPaths),
			DnfWithSourceOpts(sOpt),
			DnfInstallWithConstraints(opts),
		}

		install := cfg.Install([]string{filepath.Join(rpmMountDir, "*/*.rpm")}, installOpts...)
		return in.Run(
			dalec.WithConstraints(opts...),
			deps.GetSourceLocation(in),
			install,
		).Root()
	}
}

func (cfg *Config) DownloadDeps(sOpt dalec.SourceOpts, spec *dalec.Spec, targetKey string, constraints dalec.PackageDependencyList, opts ...llb.ConstraintsOpt) llb.State {
	if constraints == nil {
		return llb.Scratch()
	}

	opts = append(opts, dalec.ProgressGroup("Downloading dependencies"))

	worker := cfg.Worker(sOpt, dalec.Platform(sOpt.TargetPlatform), dalec.WithConstraints(opts...))

	installOpts := []DnfInstallOpt{
		DnfWithSourceOpts(sOpt),
		DnfInstallWithConstraints(opts),
	}

	worker = worker.Run(
		dalec.WithConstraints(opts...),
		cfg.Install([]string{"dnf-utils"}, installOpts...),
	).Root()

	// NOTE: dnf4 supports specifying the download destination dir as a global option (before
	// the verb) and offers multiple aliases for it (`--destdir`, `--downloaddir`).
	// dnf5 requires the destination path be specified as an argument to the `download` verb
	// and only supports the `--destdir` option name. These args work for both versions.
	args := []string{"download", "--destdir", "/output"}
	for name, constraint := range dalec.SortedMapIter(constraints) {
		if len(constraint.Version) == 0 {
			args = append(args, name)
			continue
		}
		for _, version := range constraint.Version {
			args = append(args, fmt.Sprintf("%s %s", name, rpm.FormatVersionConstraint(version)))
		}
	}

	installTimeRepos := spec.GetInstallRepos(targetKey)
	repoMounts, keyPaths := cfg.RepoMounts(installTimeRepos, sOpt, opts...)

	installOpts = append(installOpts,
		DnfWithMounts(repoMounts),
		DnfImportKeys(keyPaths),
	)

	var installCfg dnfInstallConfig
	dnfInstallOptions(&installCfg, installOpts)

	return worker.Run(
		dalec.WithRunOptions(dnfCommand(&installCfg, cfg.ReleaseVer, "dnf", nil, args)),
		dalec.WithConstraints(opts...),
	).AddMount("/output", llb.Scratch())
}
