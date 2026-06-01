#!/usr/bin/env bash

set -euxo pipefail

NAME=$1
VERSION=$2
ARCH=$3

# Map Docker/Go arch to systemd arch.
case ${ARCH} in
	arm|arm64|mips64|ppc64|s390x|sparc64|riscv64) : ;;
	mipsle|mips64le|ppc64le) ARCH=${ARCH%le}-le ;;
	386) ARCH=x86 ;;
	amd64) ARCH=x86-64 ;;
	loong64) ARCH=loongarch64 ;;
	*)
		echo "Unsupported architecture: ${ARCH}" >&2
		exit 1 ;;
esac

TMPDIR=$(mktemp -d)
trap 'rm -rf -- "${TMPDIR}"' EXIT
mkdir -p "${TMPDIR}"/usr/lib/extension-release.d

cat > "${TMPDIR}/usr/lib/extension-release.d/extension-release.${NAME}" <<-EOF
ID=_any
ARCHITECTURE=${ARCH}
EXTENSION_RELOAD_MANAGER=1
EOF

cd /input
shopt -s extglob nullglob

# Sysexts cannot include /etc, so move that data to /usr/share/${NAME}/etc and
# copy it to /etc at runtime.
for ITEM in etc/!(systemd); do
	mkdir -p "${TMPDIR}"/usr/lib/tmpfiles.d
	echo "C+ /${ITEM} - - - - /usr/share/${NAME}/${ITEM}" >> "${TMPDIR}/usr/lib/tmpfiles.d/10-${NAME}.conf"
done

# Automatically start any systemd services when the sysext is attached.
for ITEM in usr/lib/systemd/system/!(*@*).service; do
	ITEM=${ITEM##*/}
	mkdir -p "${TMPDIR}"/usr/lib/systemd/system/multi-user.target.d
	cat > "${TMPDIR}/usr/lib/systemd/system/multi-user.target.d/10-${NAME}-${ITEM%.service}.conf" <<-EOF
	[Unit]
	Upholds=${ITEM}
	EOF
done

tar \
	--create \
	--owner=root:0 \
	--group=root:0 \
	--exclude=etc/systemd \
	--xattrs-exclude=^btrfs. \
	--transform="s:^(bin|sbin|lib|lib64)/:usr/\1/:x" \
	--transform="s:^etc\b:usr/share/${NAME//:/\\:}/etc:x" \
	?(usr)/ ?(etc)/ ?(opt)/ ?(bin)/ ?(sbin)/ ?(lib)/ ?(lib64)/ \
	--directory="${TMPDIR}" \
	usr/ \
	| \
	mkfs.erofs \
		--tar=f \
		-zlz4hc \
		"/output/${NAME}-${VERSION}-${ARCH}.raw"
