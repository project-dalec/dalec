package distro

import (
	"github.com/moby/buildkit/client/llb"
	"github.com/project-dalec/dalec"
)

const rpmMinimizeScript = `#!/usr/bin/env bash
set -euo pipefail

rootfs=/tmp/rootfs

rpm_root() {
	rpm --root "${rootfs}" "$@"
}

seed_packages() {
	local dir rpm_file path

	for dir in /tmp/rpms /tmp/rpms-base; do
		[ -d "${dir}" ] || continue

		# Package output is always grouped by architecture, matching the
		# /RPMS/<arch>/*.rpm layout consumed by the installer.
		for rpm_file in "${dir}"/*/*.rpm; do
			[ -f "${rpm_file}" ] || continue
			rpm -qp --qf '%{NAME}\n' "${rpm_file}"
		done
	done

	for path in \
		/etc/passwd \
		/etc/group \
		/etc/shadow \
		/etc/gshadow \
		/etc/subuid \
		/etc/subgid \
		/etc/nsswitch.conf; do
		[ -e "${rootfs}${path}" ] || continue
		rpm_root -qf --qf '%{NAME}\n' "${path}" 2>/dev/null || true
	done
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

requirement_providers() {
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

	printf '%s\n' "${providers}"
}

rich_requirement_providers() {
	local pkg="$1"
	local providers

	# rpm --whatprovides cannot evaluate boolean requirements. Ask the
	# installed-package solver for the package's direct requirement providers.
	if providers="$(dnf -q --installroot "${rootfs}" repoquery --installed \
		--providers-of=requires --qf '%{name}\n' "${pkg}" 2>/dev/null)"; then
		printf '%s\n' "${providers}"
	elif providers="$(dnf -q --installroot "${rootfs}" repoquery --installed \
		--requires --resolve --qf '%{name}\n' "${pkg}" 2>/dev/null)"; then
		printf '%s\n' "${providers}"
	else
		echo "failed to resolve rich RPM requirements for ${pkg}" >&2
		return 1
	fi
}

declare -a queue=()
declare -A keep=()

mapfile -t seeds < <(seed_packages | sort -u)
if [ "${#seeds[@]}" -eq 0 ]; then
	echo "no RPM seed packages found for minimization" >&2
	exit 1
fi

for pkg in "${seeds[@]}"; do
	[ -n "${pkg}" ] || continue
	if rpm_root -q "${pkg}" >/dev/null 2>&1; then
		queue+=("${pkg}")
	fi
done

if [ "${#queue[@]}" -eq 0 ]; then
	echo "no installed RPM seed packages found for minimization" >&2
	exit 1
fi

queue_index=0
while [ "${queue_index}" -lt "${#queue[@]}" ]; do
	pkg="${queue[queue_index]}"
	queue_index=$((queue_index + 1))

	[ -n "${pkg}" ] || continue

	if [ -n "${keep[${pkg}]+x}" ]; then
		continue
	fi

	if ! rpm_root -q "${pkg}" >/dev/null 2>&1; then
		continue
	fi

	keep["${pkg}"]=1
	has_rich_requirements=false

	if ! requirements="$(rpm_root -q --qf '[%{REQUIRENAME}\t%{REQUIREFLAGS:deptype}\n]' "${pkg}")"; then
		echo "failed to read RPM requirements for ${pkg}" >&2
		exit 1
	fi

	while IFS=$'\t' read -r req flags; do
		[ -n "${req}" ] || continue
		if is_scriptlet_requirement "${flags:-}"; then
			continue
		fi
		if [[ "${req}" == \(* ]]; then
			has_rich_requirements=true
			continue
		fi

		providers="$(requirement_providers "${req}")" || exit 1
		while IFS= read -r provider; do
			[ -n "${provider}" ] && queue+=("${provider}")
		done <<< "${providers}"
	done <<< "${requirements}"

	if "${has_rich_requirements}"; then
		providers="$(rich_requirement_providers "${pkg}")" || exit 1
		while IFS= read -r provider; do
			[ -n "${provider}" ] && queue+=("${provider}")
		done <<< "${providers}"
	fi
done

mapfile -t installed < <(rpm_root -qa --qf '%{NAME}\n' | sort -u)
declare -a remove=()
for pkg in "${installed[@]}"; do
	[ -n "${pkg}" ] || continue
	if [ -z "${keep[${pkg}]+x}" ]; then
		remove+=("${pkg}")
	fi
done

echo "DALEC RPM keep set:" >&2
printf '%s\n' "${!keep[@]}" | sort | sed 's/^/  /' >&2

if [ "${#remove[@]}" -gt 0 ]; then
	echo "DALEC RPM packages removed during minimization:"
	printf '%s\n' "${remove[@]}" | sed 's/^/  /' >&2

	remove_specs="$(
		for pkg in "${remove[@]}"; do
			rpm_root -q --qf '%{NAME}\t%{VERSION}\t%{RELEASE}\t%{ARCH}\n' "${pkg}" 2>/dev/null \
				| while IFS=$'\t' read -r name version release arch; do
					[ -n "${name}" ] || continue

					if [ "${arch}" = "(none)" ]; then
						printf '%s-%s-%s\n' "${name}" "${version}" "${release}"
						continue
					fi

					printf '%s-%s-%s.%s\n' "${name}" "${version}" "${release}" "${arch}"
				done
		done | sort -u
	)"

	printf '%s\n' "${remove_specs}" | xargs -r rpm --root "${rootfs}" -e --noscripts --notriggers --nodeps
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
done < <(printf '%s\n' "${!keep[@]}")

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
