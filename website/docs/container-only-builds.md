---
title: Container-only builds
---

Dalec can create minimal container images with only specific packages installed, without building from source code. This is useful for creating minimal container images with just the runtime dependencies you need.

## How it Works

When you specify only runtime dependencies in a Dalec spec (no sources or build steps), Dalec creates a [Virtual Package](virtual-packages.md) and installs it in the target base image. The result is a minimal container with only your specified packages and their dependencies.

## Example: Minimal Image with curl and bash

```yaml
# syntax=ghcr.io/project-dalec/dalec/frontend:latest
name: my-minimal-image
version: 0.1.0
revision: 1
license: MIT
description: A minimal image with only curl and shell access

dependencies:
  runtime:
    curl:
    bash:

image:
  entrypoint: /bin/bash
```

Build the container:

```shell
docker build -f my-minimal-image.yml --target=azlinux3 -t my-minimal-image:0.1.0 .
```

This produces a minimal image built from `scratch` containing:

- `curl` and `bash`
- Essential packages like `prebuilt-ca-certificates` and `tzdata`
- Dependencies of the specified packages

The `--target=azlinux3` flag tells Dalec to use Azure Linux 3 repositories for package installation, even though the final image starts from scratch.

:::tip

Alternatively, you can omit creating a Dalec spec file by passing the dependencies directly in the command line. This is useful for quick builds without needing a spec file.

```bash
docker build -t my-minimal-image:0.1.0 --build-arg BUILDKIT_SYNTAX=ghcr.io/project-dalec/dalec/frontend:latest --target=azlinux3/container/depsonly -<<<"$(jq -c '.dependencies.runtime = {"curl": {}, "bash": {}} | .image.entrypoint = "/bin/bash"' <<<"{}" )"
```

:::

## Cleanup policy for Debian/Ubuntu minimal images

Debian- and Ubuntu-based minimal container targets (for example `trixie/container`, `bookworm/container`, `noble/container`) run a post-install cleanup pass that strips the image down to just what the spec needs. The policy is intentionally aggressive and **all-or-nothing per directory tree**:

- **`/usr/share/doc`, `/usr/share/man`, `/usr/share/info`** — preserved *only* when the spec declares at least one [`docs`](artifacts.md#docs), [`manpages`](artifacts.md#manpages), or [`licenses`](artifacts.md#licenses) artifact. If preserved, **all** dependency-owned content under those paths is also retained (the cleanup does not attempt to filter dependency files). If pruned, **all** content under those paths is removed, including any manpages or `copyright` files shipped by runtime dependencies.
- **`/etc/systemd`, `/var/lib/systemd`** — preserved only when the final image actually has the `systemd` package installed (or `systemctl` on `PATH`).
- **`/var/log`** — the directory itself is always preserved, but its contents are emptied. Many runtime processes assume `/var/log` exists.
- **Package manager state and caches** (`/etc/apt`, `/var/cache/apt`, `/var/lib/apt`, `/usr/lib/apt`, `/var/lib/pam`, `/var/cache/debconf`, `/usr/share/{bash-completion,bug,debconf,lintian,locale}`) — always removed.

### Implication for spec authors

If you want the final image to ship with manpages, the changelog, or copyright files for **any** package — your own or a dependency's — declare at least one `docs`, `manpages`, or `licenses` artifact in your spec. A single license file is enough to flip the toggle:

```yaml
artifacts:
  licenses:
    LICENSE:
```

This preserves `/usr/share/doc` wholesale, so dependency-shipped `copyright` files (and any other dependency-owned content under the doc/man/info trees) will also remain in the image.

If you specifically want a leaner image with no dependency-owned docs but you need your own license files present, that is still the result of declaring `licenses` — the cleanup script does not currently support a more granular policy. If you need full control over which files are kept, use the non-minimal container target instead (for example `trixie/testing/container`), where this cleanup pass does not run.
