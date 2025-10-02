---
title: Quickstart
---

This guide shows you how to build packages and containers from source using Dalec.

## What is Dalec?

Dalec is a [Docker Buildkit frontend](https://docs.docker.com/build/buildkit/frontend/) that translates a YAML spec into build instructions for creating packages (RPMs, DEBs) and container images. It requires only Docker to run.

:::note
All Dalec spec files must start with `# syntax=ghcr.io/project-dalec/dalec/frontend:latest` to tell buildkit which frontend to use.
:::


## How it Works

Dalec builds happen in stages:

1. **Package Build** - Check out sources and build packages using your defined build steps
1. **Package Test** - Install and test the package in a clean environment
1. **Create Container** (optional) - Install the package(s) into a scratch image to create a container


## Example: Building go-md2man

Let's build the `go-md2man` package from source. We need:

1. Sources to pull from
2. Build instructions
3. Artifacts to include in the package

Here's the complete Dalec spec file:

```yaml
# syntax=ghcr.io/project-dalec/dalec/frontend:latest
name: go-md2man
version: 2.1.0
revision: "1"
packager: Dalec Example
vendor: Dalec Example
license: MIT
description: A tool to convert markdown into man pages (roff).
website: https://github.com/cpuguy83/go-md2man

sources:
  src:
    generate:
      - gomod: {}  # Pre-downloads Go modules since network is disabled during build
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: "v2.1.0"

dependencies:
  build:
    golang:

build:
  env:
    CGO_ENABLED: "0"
  steps:
    - command: |
        cd src
        go build -o go-md2man .

artifacts:
  binaries:
    src/go-md2man:

image:
  entrypoint: go-md2man
  cmd: --help

tests:
  - name: Check bin
    files:
      /usr/bin/go-md2man:
        permissions: 0755
```

:::tip
Need to swap in a fork or update to a newer dependency? Add a `replace` or `require` entry under the `gomod` generator to rewrite `go.mod` before Dalec downloads modules:

- **`replace`** mirrors `go mod edit -replace` and redirects a module (optionally tied to a version) to an alternate module path or local checkout.
- **`require`** mirrors `go mod edit -require` and adds or pins the dependency that appears in `go.mod`. The shorthand `module:target@version` lets you keep the original module name while sourcing a forked `target` path.

```yaml
sources:
  src:
    generate:
      - gomod:
          replace:
            - github.com/example/mod@v1.1.0:../patched/mod
          require:
            - github.com/example/mod:github.com/example/mod@v1.2.3
```

:::

Key sections explained:

- **Metadata**: Package name, version, license, and description (see [spec](spec.md))
- **Sources**: Git repository to clone, with `generate` to pre-download Go modules (see [sources](sources.md))
- **Dependencies**: Build-time dependencies (golang) (see [spec](spec.md#dependencies-section))
- **Build**: Environment variables and build commands (see [spec](spec.md#build-section))
- **Artifacts**: Files to include in the package (see [artifacts](artifacts.md))
- **Image**: Container entrypoint and default command (see [spec](spec.md#image-section))
- **Tests**: Validation that the package was built correctly (see [testing](testing.md))

## Building the Package

### Build an RPM package

```shell
docker build -t go-md2man:2.1.0 -f docs/examples/go-md2man.yml --target=azlinux3/rpm --output=_output .
```

This creates `RPM` and `SRPM` directories in `_output/` with the built packages.

### Build a container image

```shell
docker build -t go-md2man:2.1.0 -f docs/examples/go-md2man.yml --target=azlinux3 .
```

This produces a container image named `go-md2man:2.1.0`.

:::note
See the [targets](targets.md) section for all available build targets.
:::
