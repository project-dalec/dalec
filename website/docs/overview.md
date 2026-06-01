---
title: Overview
slug: /
---

## What is Dalec?

Dalec is a [Docker Buildkit frontend](https://docs.docker.com/build/buildkit/frontend/) that translates declarative YAML specifications into build instructions. Think of it as a specialized compiler that takes your package configuration and produces packages and containers across multiple Linux distributions and Windows.

## Why Use Dalec?

- **Unified Build Process**: Write once, build for multiple targets (Debian, Ubuntu, Azure Linux, Rocky Linux, etc.)
- **Supply Chain Security**: Built-in support for SBOMs, provenance attestations, and package signing
- **Docker-Native**: Leverages Docker Buildkit's caching, parallelization, and security features
- **Cross-Platform**: Build packages for different architectures and operating systems

## Features

- 🐳 No additional tools needed except [Docker](https://docs.docker.com/engine/install/)
- 🚀 Easy to use declarative configuration
- 📦 Build packages, containers and/or system extensions for multiple [targets](targets.md)
  - DEB-based: Debian and Ubuntu
  - RPM-based: Azure Linux, Rocky Linux, and Alma Linux
  - Windows containers (cross compilation only)
- 🔌 Pluggable support for other operating systems
- 🤏 Minimal image size, resulting in fewer vulnerabilities and smaller attack surface
- 🪟 Support for Windows containers
- ✍️ Support for signed packages
- 🔐 Supply chain security with build-time SBOMs and provenance attestations

## Getting Started

To start building with Dalec, you'll need [Docker](https://docs.docker.com/engine/install/) installed.

👉 **Ready to build?** See the [Quickstart](quickstart.md) guide!

**Using VSCode?** Install the [Dalec extension](https://github.com/project-dalec/dalec-vscode-extension) for schema validation and IntelliSense.

## Build Types

### Building from Source

Dalec can build packages and containers from source code repositories by specifying the source repository and build steps in the Dalec spec. The source code is checked out, built, and the resulting artifacts are included in the package.

For more information, see [Quick Start](quickstart.md).

### Virtual Packages

Create "virtual" packages that reference other packages but don't install files themselves. This is useful for creating packages that are collections of dependencies.

For more information, see [Virtual Packages](virtual-packages.md).

## Dalec Specification

The Dalec spec is a YAML file that describes:

- Package metadata (name, version, license, etc.)
- Build and runtime dependencies
- Source repositories and build steps
- Files to include in the package
- Container image customizations

For complete details, see the [Dalec Specification](spec.md).
