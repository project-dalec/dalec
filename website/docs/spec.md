---
title: Dalec Specification
---

Dalec YAML specification is a declarative format for building system packages and containers from those packages.

This section provides a high level overview of the Dalec YAML specification. For more detailed information, please see the applicable sections.

:::note
All Dalec spec YAMLs must start with `# syntax=ghcr.io/project-dalec/dalec/frontend:latest`.
:::

Dalec spec YAMLs are composed of the following sections:
- [Args section](#args-section)
- [Metadata section](#metadata-section)
- [Targets section](#targets-section)
  - [Image section](#image-section)
  - [Package Config section](#package-config-section)
- [Sources section](#sources-section)
- [Dependencies section](#dependencies-section)
- [Build section](#build-section)
- [Artifacts section](#artifacts-section)
- [Tests section](#tests-section)
- [ Changelog section](#changelog-section)

## Args section

Args section is an optional section that is used to define the arguments that can be passed to the spec. These arguments can be used in the spec to define the version, commit, or any other fields.

```yaml
args:
  VERSION: 1.0.0
  COMMIT: 55019c83b0fd51ef4ced8c29eec2c4847f896e74
  REVISION: 1
```

There are a few built-in arguments which, if present, Dalec will substitute values for. They are listed as examples in the following block:

```yaml
args:
  TARGETOS:
  TARGETARCH:
  TARGETPLATFORM:
  TARGETVARIANT:
  DALEC_TARGET:
```

These arguments are set based on the default docker platform for the machine, *unless* the platform is overridden explicitly in the docker build with `--platform`. For example, upon invoking `docker build` on a Linux amd64 machine, we would have `TARGETOS=linux`, `TARGETARCH=amd64`, `TARGETPLATFORM=linux/amd64`.

`DALEC_TARGET` is set to the target name, such as `mariner2`, `azlinux3`, or `windowscross`.

:::note
No default value should be included for these build args. These args are opt-in. If you haven't listed them in the args section as shown above, Dalec will **not** substitute values for them.
:::

## Metadata section

Metadata section is used to define the metadata of the spec. This metadata includes the name, packager, vendor, license, website, and description of the spec.

```yaml
name: My-Package
license: Apache-2.0
description: This is a sample package
version: ${VERSION}
revision: ${REVISION}
packager: Dalec Authors
vendor: Dalec Authors
website: https://github.com/foo/bar
```

- `name`: The name of the package.
- `license`: The license of the package.
- `description`: The description of the package.
- `version`: The version of the package.
- `revision`: The revision of the package.
- `packager`: The packager of the package. This is an optional field.
- `vendor`: The vendor of the package. This is an optional field.
- `website`: The website of the package. This is an optional field.

:::tip
Any field at the top-level that begins with `x-` will be ignored by Dalec. This allows for custom fields to be added to the spec. For example, `x-foo: bar`. Any other field that is not recognized by Dalec will result in a validation error.
:::

### Additional metadata

#### conflicts

The `conflicts` field is used to define the packages that conflict with this package. This is an optional field.
This is required when this package provides one or more files that are provided by another package.
This helps the package manager know when two packages cannot be installed at the same time.

```yaml
conflicts:
  foo:
  bar:
    # Add version constraints to the conflicting package
    version:
      - ">=1.0.0"
      - "<2.0.0"
```

#### provides

The `provides` field is used to define the packages that are provided by this package. This is an optional field.
This is useful when the package name may not align with a more common name for the package or when the package is a
replacement for another package.

```yaml
  provides:
    foo:
    bar:
      # With version constraints
      version:
        - = 1.0.0
```

#### replaces

The `replaces` field is used to define the packages that are replaced by this package. This is an optional field.
This is useful when this package should be chosen over another package with a different name and can be combined with
`conflicts` to ensure that the other package is removed when this package is installed.

```yaml
  replaces:
    foo:
    bar:
      # With version constraints
      version:
        - < 1.0.0
```

## Targets section

Targets section is used to define configuration per target. Each target can have its own configuration.

```yaml
targets:
  mariner2:
    image:
      base: mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0
      post:
        symlinks:
          /usr/bin/my-binary:
            paths:
              - /my-binary
    package_config:
      signer:
        image: example.com/signer:latest
  azlinux3:
    # same fields as above
  windowscross:
    # same fields as above
```

Targets can either be built-in or reference an external image reference that
handles the target build.

For more information, please see [Targets](targets.md).

### Image section

Image section is used to define the base image and post processing for the image.

Example:

```yaml
image:
  base: mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0
  post:
    symlinks:
      /usr/bin/my-binary:
       paths:
         - /my-binary
  entrypoint: /my-binary
```

For more information, please see [Images](image.md).

### Package Config section

Package Config section is used to define the package configuration for the target.

- `signer`: The signer configuration for the package. This is used to sign the package. For more information, please see [Signing Packages](signing.md).

## Sources section

Sources section is used to define the sources for the spec. These sources can be used to define the source code, patches, or any other files needed for the spec.

```yaml
sources:
  foo:
    git:
      url: https://github.com/foo/bar.git
      commit: ${COMMIT}
      keepGitDir: true
    generate:
    - gomod: {}
  foo-patch:
    http:
      url: https://example.com/foo.patch
  foo-inline:
    inline:
      - name: my-script
        content: |
          #!/bin/sh
          echo "Hello, World!"
  foo-context:
    context: {}
```

For more information, please see [Sources](sources.md).

## Dependencies section

The dependencies section is used to define the dependencies for the spec. These dependencies can be used to define the build dependencies, runtime dependencies, or any other dependencies needed for the package.

:::tip
Dependencies can be defined at the root level or under a target. If defined under a target, the dependencies will only be used for that target. Dependencies under a target will override dependencies at the root level. For more information, please see [Targets](targets.md).
:::

```yaml
dependencies:
  build:
    - golang
    - gcc
  runtime:
    - libfoo
    - libbar
  recommends:
    - libcafe
  test:
    - kind
  extra_repos:
    - libdecaf
```

`Build` dependencies are the list of packages required to build the package.

`Runtime` dependencies are the list of packages required to install/run the package.

`Recommends` dependencies are the list of packages recommended to install with the generated package.

`Test` dependencies list packages required for running tests. These packages are only installed for tests which have steps that require running a command in the built container.

See [dependencies](dependencies.md) for more details on dependency options.

Sometimes you may need to add extra repositories in order to fulfill the
specified dependencies.
You can do this by adding these to the `extra_repos` field.
The `extra_repos` field takes a list of repository configurations with optional
public key data and optional repo data (e.g. the actual data of a repository).
See [repositories](repositories.md) for more details on repository configs

## Build section

Build section is used to define the build steps for the spec. These build steps can be used to define the build commands, environment variables, or any other build configuration needed for the package.

```yaml
build:
  env:
    TAG: v${VERSION}
    GOPROXY: direct
    CGO_ENABLED: "0"
    GOOS: ${TARGETOS}
  caches:
    - gobuild:
    - dir:
      key: my_key
      dest: /my/cache/dir
  steps:
    - command: |
        go build -ldflags "-s -w -X github.com/foo/bar/version.Version=${TAG}" -o /out/my-binary ./cmd/my-binary
```

- `env`: The environment variables for the build.
- `steps`: The build steps for the package.
- `network_mode`: Set the network mode to use for build steps (accepts: empty, `none`, `sandbox`)
- `cache`: Configure caches which persist between builds. See [caches](caches.md) for more details.

:::tip
TARGETOS is a built-in argument that Dalec will substitute with the target OS value. For more information, please see [Args section](#args-section).
:::

:::tip
Set `network_mode` to `sandbox` to allow internet access during build
:::

The input of a build is a directory with all [sources](#sources-section) laid out based on their source name.
The output of a build is the content of the input with all the provided steps executed against it.
A spec is not required to define a build section, in which case the output is the same as the input.
This feeds directly into the [artifacts section](#artifacts-section).

## Artifacts section

Artifacts section is used to define the artifacts for the spec. These artifacts can be used to define the output of the build, such as the package or container image.

```yaml
artifacts:
  binaries:
     foo/my-binary: {}
  manpages:
    src/man/man8/*:
      subpath: man8
```

For more information, please see [Artifacts](artifacts.md)

## Tests section

Tests section is used to define the tests for the spec. These tests can be used to define the test cases, steps, or any other tests needed for the package.

```yaml
tests:
  - name: check permissions
    files:
      /usr/bin/my-binary:
        permissions: 0755
  - name: version reporting
    steps:
      - command: my-binary --version
        stdout:
          starts_with: "my-binary version ${VERSION}"
          contains:
            - "libseccomp: "
        stderr:
          empty: true
```

For more information, please see [Testing](testing.md).

## Changelog section

Changelog section is used to define the changelog for the spec. This changelog can be used to define the changes made to the package which may include patches,
updating source versions, triggering rebuilds to update dependencies, or basically any change to the spec that will trigger a new revision of the package.

The channgelog is stored in the package metadata, where supported, and as such can be viewed by the package manager tooling.

```yaml
changelog:
  - author: John Doe <john.doe@example.com>
    date: 2025-04-24
    changes:
      - "Update build dependency on libfoo to version 2.0"
      - Update upstream source to version 1.1.0
```
