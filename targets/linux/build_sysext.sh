#!/usr/bin/env bash

set -euxo pipefail
shopt -s extglob nullglob

NAME=$1
VERSION=$2
ARCH=$3

# Default basename is Dalec’s historical output: NAME-VERSION-ARCH
IMAGE_BASENAME="${NAME}-${VERSION}-${ARCH}"

# Allow overriding the output basename (e.g. Flatcar expects /etc/extensions/<name>.raw).
# If user passes "foo.raw", normalize to "foo".
IMAGE_NAME="${DALEC_SYSEXT_IMAGE_NAME:-${IMAGE_BASENAME}}"
IMAGE_NAME="${IMAGE_NAME%.raw}"

# Matching knobs (Flatcar commonly wants ID=flatcar + VERSION_ID or SYSEXT_LEVEL).
# Defaults preserve current behavior.
OS_ID="${DALEC_SYSEXT_OS_ID:-_any}"
OS_VERSION_ID="${DALEC_SYSEXT_OS_VERSION_ID:-}"
SYSEXT_LEVEL="${DALEC_SYSEXT_SYSEXT_LEVEL:-}"

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
REL_DIR="${TMPDIR}/usr/lib/extension-release.d"
mkdir -p "${REL_DIR}"

BASE_REL="extension-release.${NAME}"
BASE_PATH="${REL_DIR}/${BASE_REL}"

cat > "${BASE_PATH}" <<-EOF
ID=${OS_ID}
ARCHITECTURE=${ARCH}
EXTENSION_RELOAD_MANAGER=1
EOF

[[ -n "${OS_VERSION_ID}" ]] && echo "VERSION_ID=${OS_VERSION_ID}" >> "${BASE_PATH}"
[[ -n "${SYSEXT_LEVEL}" ]] && echo "SYSEXT_LEVEL=${SYSEXT_LEVEL}" >> "${BASE_PATH}"



# systemd-sysext expects extension-release.<IMAGE> where <IMAGE> matches the image basename.
# Provide a matching entry for the produced image name.
if [ "${IMAGE_NAME}" != "${NAME}" ]; then
        ln -sf "${BASE_REL}" "${REL_DIR}/extension-release.${IMAGE_NAME}"
fi


cd /input

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
		"/output/${IMAGE_NAME}.raw"
