---
title: System Extensions
---

:::caution

Experimental: This feature might change without preserving backwards compatibility.

:::

Dalec can create system extensions, known as sysexts, for use with systemd. Such extensions are filesystem images that get overlaid onto an existing running system. These are often designed to be standalone, making them very portable, but they don't necessarily have to be.

## How it Works

System extensions satisfy a different use case to containers. Whether a extension should be standalone and exactly which dependencies it should include ultimately depends on where it will be merged in practise. As such, a sysext-specific dependency type is used by Dalec to give you full control over what is included.

The system extension is created from packages, either built from the same spec or elsewhere, but unlike with containers, the packages are extracted directly rather than installed by a package manager. Consequently, no dependency resolution is performed, and no package metadata is written.

The result is a single [EROFS](https://erofs.docs.kernel.org) image file. This can be merged against just about any system running systemd 248 or later.

## Example: System extension with jq and zsh

```yaml
# syntax=ghcr.io/project-dalec/dalec/frontend:latest
name: my-sysext
version: 0.1.0
revision: 1
license: MIT
description: A system extension with some useful tools

dependencies:
  sysext:
    jq:
    zsh:
```

Build the container:

```shell
docker build -f my-sysext.yml --target=azlinux3/testing/sysext --output=. .
```

This creates `my-sysext-v0.1.0-1-azlinux3-x86-64.raw` in the current directory. You can merge this with `systemd-sysext` or just mount it to some directory with `mount -o loop`. It contains:

- `jq`, `zsh` and their associated files
- Some system extension metadata
- That's it!

The `--target=azlinux3` flag tells Dalec to source the packages from Azure Linux 3 repositories, but you can merge the extension against other distributions.

## Tips

### glibc compatibility

In the above example, the binaries are dynamically linked against glibc from Azure Linux 3. Bear in mind that glibc is not forwards-compatible, so while you can merge the extension against other distributions, these binaries will not work against older releases such as Ubuntu 22.04.

### Dependencies

In the above example, jq also requires `libonig.so.5` at the time of writing. This library is unlikely to be present on most systems, so it should either be installed before merging or included with the extension by adding `oniguruma` under `sysext`. Do whatever is most appropriate for your use case.

### Conflicts

Files on the running system will be overshadowed by those in the system extension if they have the same path. Care must therefore be taken to ensure you don't break the running system by including incompatible files. For example, it is vitally important that you don't include glibc unless it has been installed to an alternative path. Note that the running system is not modified by the merge, and the original files become visible again when unmerging.

### Static linking

The above issues can be largely avoided by including statically-linked rather than dynamically-linked binaries. This will make your system extension much more portable. However, binaries shipped by distributions are practically always dynamic, so this will require you to build the binaries and their dependencies yourself as part of the spec.

### Prefixing

If conflicts are likely to occur, even when static linking, the extension can place its files under a unique prefix, e.g. `/usr/local/my-sysext`. For this to work, the binaries should be built specifically for this prefix, e.g. `./configure --prefix=/usr/local/my-sysext`. Dynamically-linked binaries should explicitly include somewhere like `/usr/local/my-sysext/lib` in the `RUNPATH` to avoid the need for wrappers that set `LD_LIBRARY_PATH`. If executables rely on other executables, then wrappers are still needed to add `/usr/local/my-sysext/bin` to the `PATH`.

### Permitted paths

System extensions will only merge files under `/usr` and `/opt`. Files located elsewhere will not appear once merged. For this reason, `/bin`, `/sbin`, `/lib` and `/lib64` are automatically relocated under `/usr` by Dalec.

systemd supports configuration extensions, known as confexts, for handling `/etc`, but these are not yet supported by Dalec. In the meantime, anything installed under `/etc` by the spec is relocated under `/usr/share/<NAME>/etc`, and a tmpfiles.d entry is created to copy these files to `/etc` when the extension is merged. They are not removed when the extension is unmerged.

### Ownership

The UIDs and GIDs of the running system may not match those used to build the extension, so Dalec automatically changes all user and group ownership to `root:root` when creating the extension.
