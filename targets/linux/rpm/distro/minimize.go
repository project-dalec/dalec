package distro

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const rpmMinimizeScript = `#!/usr/bin/env bash
set -euo pipefail

rootfs=/tmp/rootfs
workdir=/tmp/dalec-rpm-minimize
seed_file="${workdir}/seeds"
queue_file="${workdir}/queue"
keep_file="${workdir}/keep"
installed_file="${workdir}/installed"
remove_file="${workdir}/remove"
remove_specs_file="${workdir}/remove-specs"
requires_file="${workdir}/requires"

mkdir -p "${workdir}"
: > "${seed_file}"
: > "${queue_file}"
: > "${keep_file}"

rpm_root() {
	rpm --root "${rootfs}" "$@"
}

seed_from_rpms() {
	local dir="$1"

	[ -d "${dir}" ] || return 0

	while IFS= read -r -d '' rpm_file; do
		rpm -qp --qf '%{NAME}\n' "${rpm_file}" >> "${seed_file}"
	done < <(find "${dir}" -type f -name '*.rpm' -print0)
}

is_scriptlet_requirement() {
	local flags="$1"

	case "${flags}" in
		*interp*|*pre*|*post*|*preun*|*postun*|*pretrans*|*posttrans*|*trigger*|*verify*)
			return 0
			;;
	esac

	return 1
}

add_installed_seed() {
	local pkg="$1"

	[ -n "${pkg}" ] || return 0

	if rpm_root -q --qf '%{NAME}\n' "${pkg}" >/dev/null 2>&1; then
		printf '%s\n' "${pkg}" >> "${queue_file}"
	fi
}

add_requirement_providers() {
	local req="$1"
	local providers

	[ -n "${req}" ] || return 0

	case "${req}" in
		rpmlib\(*|\(none\))
			return 0
			;;
	esac

	if ! providers="$(rpm_root -q --whatprovides --qf '%{NAME}\n' "${req}" 2>/dev/null | sed '/^$/d')"; then
		echo "required RPM dependency ${req} has no installed provider" >&2
		return 1
	fi

	if [ -z "${providers}" ]; then
		echo "required RPM dependency ${req} has no installed provider" >&2
		return 1
	fi

	printf '%s\n' "${providers}" >> "${queue_file}"
}

add_remove_specs() {
	local pkg="$1"

	rpm_root -q --qf '%{NAME}\t%{VERSION}\t%{RELEASE}\t%{ARCH}\n' "${pkg}" 2>/dev/null \
		| while IFS=$'\t' read -r name version release arch; do
			[ -n "${name}" ] || continue

			if [ "${arch}" = "(none)" ]; then
				printf '%s-%s-%s\n' "${name}" "${version}" "${release}"
				continue
			fi

			printf '%s-%s-%s.%s\n' "${name}" "${version}" "${release}" "${arch}"
		done >> "${remove_specs_file}"
}

seed_from_rpms /tmp/rpms
seed_from_rpms /tmp/rpms-base

sort -u "${seed_file}" -o "${seed_file}"
if [ ! -s "${seed_file}" ]; then
	echo "no RPM seed packages found for minimization" >&2
	exit 1
fi

while IFS= read -r pkg; do
	add_installed_seed "${pkg}"
done < "${seed_file}"

if [ ! -s "${queue_file}" ]; then
	echo "no installed RPM seed packages found for minimization" >&2
	exit 1
fi

while [ -s "${queue_file}" ]; do
	pkg="$(head -n1 "${queue_file}")"
	tail -n +2 "${queue_file}" > "${queue_file}.next"
	mv "${queue_file}.next" "${queue_file}"

	[ -n "${pkg}" ] || continue

	if grep -Fxq "${pkg}" "${keep_file}"; then
		continue
	fi

	if ! rpm_root -q "${pkg}" >/dev/null 2>&1; then
		continue
	fi

	printf '%s\n' "${pkg}" >> "${keep_file}"

	if ! rpm_root -q --qf '[%{REQUIRENAME}\t%{REQUIREFLAGS:deptype}\n]' "${pkg}" > "${requires_file}"; then
		echo "failed to read RPM requirements for ${pkg}" >&2
		exit 1
	fi

	while IFS=$'\t' read -r req flags; do
		[ -n "${req}" ] || continue
		if is_scriptlet_requirement "${flags:-}"; then
			continue
		fi

		add_requirement_providers "${req}"
	done < "${requires_file}"
done

sort -u "${keep_file}" -o "${keep_file}"
rpm_root -qa --qf '%{NAME}\n' | sort -u > "${installed_file}"

comm -23 "${installed_file}" "${keep_file}" > "${remove_file}"

echo "DALEC RPM keep set:"
sed 's/^/  /' "${keep_file}" >&2 || true

if [ -s "${remove_file}" ]; then
	echo "DALEC RPM packages removed during minimization:"
	sed 's/^/  /' "${remove_file}" >&2 || true

	: > "${remove_specs_file}"
	while IFS= read -r pkg; do
		add_remove_specs "${pkg}"
	done < "${remove_file}"
	sort -u "${remove_specs_file}" -o "${remove_specs_file}"

	xargs -r rpm --root "${rootfs}" -e --noscripts --notriggers --nodeps < "${remove_specs_file}"
fi

if [ -d "${rootfs}/usr/lib/sysimage/rpm" ] && [ ! -e "${rootfs}/var/lib/rpm" ]; then
	mkdir -p "${rootfs}/var/lib"
	ln -s ../../usr/lib/sysimage/rpm "${rootfs}/var/lib/rpm"
fi

rpm_root -qa >/dev/null

while IFS= read -r pkg; do
	if ! rpm_root -q "${pkg}" >/dev/null 2>&1; then
		echo "required package ${pkg} is missing after RPM minimization" >&2
		exit 1
	fi
done < "${keep_file}"

rm -rf \
	"${rootfs}/var/cache/dnf" \
	"${rootfs}/var/cache/libdnf5" \
	"${rootfs}/var/cache/tdnf" \
	"${rootfs}/var/cache/yum" \
	"${rootfs}/var/lib/dnf" \
	"${rootfs}/var/lib/yum" \
	"${rootfs}/var/log/dnf.log" \
	"${rootfs}/var/log/dnf.librepo.log" \
	"${rootfs}/var/log/hawkey.log" \
	"${rootfs}/var/log/tdnf.log" \
	"${rootfs}/var/log/yum.log"
`

func minimizeContainer(rootfs, worker, rpmDir, basePkgs llb.State, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Minimize RPM container"))

	const (
		workPath      = "/tmp/rootfs"
		scriptPath    = "/tmp/dalec/internal/rpm/minimize.sh"
		rpmMountDir   = "/tmp/rpms"
		baseMountPath = "/tmp/rpms-base"
	)

	script := llb.Scratch().File(llb.Mkfile("minimize.sh", 0o755, []byte(rpmMinimizeScript)), opts...)

	return worker.Run(
		dalec.WithConstraints(opts...),
		llb.AddMount(scriptPath, script, llb.SourcePath("minimize.sh"), llb.Readonly),
		llb.AddMount(rpmMountDir, rpmDir, llb.SourcePath("/RPMS"), llb.Readonly),
		llb.AddMount(baseMountPath, basePkgs, llb.SourcePath("/RPMS"), llb.Readonly),
		llb.Args([]string{scriptPath}),
	).AddMount(workPath, rootfs)
}

func squashContainer(rootfs llb.State, opts ...llb.ConstraintsOpt) llb.State {
	opts = append(opts, dalec.ProgressGroup("Squash RPM container"))

	return llb.Scratch().File(llb.Copy(rootfs, "/", "/", &llb.CopyInfo{
		CopyDirContentsOnly: true,
		CreateDestPath:      true,
		AllowWildcard:       true,
	}), opts...)
}
