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
