---
title: Image
sidebar:
  exclude: true
---

Image is a field in the DALEC spec that allows you to customize certain aspects
of the produced image. The image field is an object with the following properties:

- `base`: The image ref to use as the base for the output container. [Deprecated: use `bases` instead] [base section](#base)
- `bases`: The list of base images to use as the base for the output container(s). [bases section](#bases)
- `post`: The post processing for the image, such as symlinks. [post section](#post)
- `minimization_profile`: An optional post-install minimization policy. [minimization profile section](#minimization-profile)
- `labels`: The labels for the image. This is an optional field. [labels section](#labels)
- `env`: The environment variables for the image. This is an optional field. [env section](#env)
- `entrypoint`: The entrypoint for the image. This is an optional field. [entrypoint section](#entrypoint)
- `cmd`: The command for the image. This is an optional field. [cmd section](#cmd)
- `workdir`: The working directory for the image. This is an optional field. [workdir section](#workdir)
- `user`: The user for the image. This is an optional field. [user section](#user)
- `stop_signal`: The stop signal for the image. This is an optional field. [stop signal section](#stop-signal)
- `volumes`: The volumes for the image. This is an optional field. [volumes section](#volumes)


{{< callout type="info" >}}
The `base` field is deprecated. Use the `bases` field instead.

For `bases`, the requested build target may not support multiple base images.
In this cases the target will produce an error.

Currently only `windowscross/container` supports multiple base images for the
purpose of building for multiple windows versions.
If multiple bases are provided with the same `os.version` value in the image
platform, this may produce an error or at least unexpected results since images
are keyed on the image platform metadata.
{{< /callout >}}

With the exception of `base`, `bases`, and `post`, these fields are all used to
merge with the configured (or default) base image(s).

### Minimization profile

Image minimization is opt-in. Set `minimization_profile` to `default` to enable
the target's default package minimization policy. An empty value leaves the
container unchanged. The RPM container policy preserves the RPM database while
removing packages outside the runtime dependency closure during the container
setup operation. Custom base images are not minimized.

Example:

```yaml
image:
  minimization_profile: default
```

### Layers

`layers` controls how the container root filesystem is stored as image layers.
An empty value or `single` writes one layer, which is the default.

Set `layers` to `per-package` to write one layer per installed package (or group
of related packages) plus a final layer with the package database and files that
are generated or modified during install:

```yaml
image:
  layers: per-package
```

You can also set it for a single build with
`--build-arg DALEC_IMAGE_LAYERS=per-package`, which overrides the spec.

With per-package layers, updating one package only changes that package's layer
and the final layer. Every other layer stays byte-for-byte identical, so
registries and machines that already have it don't upload or download it again.
For example, rebuilding the image to pick up an `openssl` fix (see
[Prebuilt packages](targets.md#prebuilt-packages)) only replaces the `openssl`
layer and the final layer.

How files are split:

- Files owned by a package go into that package's layer. Packages from the same
  source package (for example `openssl` and `openssl-libs`) share a layer.
  Each package built from the spec gets its own layer. Hardlinked files whose
  paths belong to different packages go into the final layer, so they stay linked.
- The package database, files not owned by any package, `%ghost` files and files
  changed after install (as reported by `rpm -V`) go into the final layer.
- Directory timestamps in the layers are set to `SOURCE_DATE_EPOCH` when it is
  provided, or to 0 otherwise, so identical packages produce identical layers.
- Images are limited to 64 layers. When there are more packages, the smallest
  ones are grouped into a single `misc` layer.

Each layer has an image history entry whose comment records its contents, for
example `dalec.layer.v1 kind=os name=openssl packages=openssl,openssl-libs`.
The final layer is recorded as `dalec.layer.v1 kind=state name=state`, and the
image gets the label `dev.dalec.image.layers=per-package`. Tools such as image
patchers can use these to find which layer holds a package.

Every build checks that the layers together contain exactly the same files,
with the same contents, permissions, ownership and extended attributes, as the
installed root filesystem, and fails if they don't.

#### Updating an existing image

To pick up package updates (for example a security fix in `openssl`) without
installing every package again, pass the image you already built as the
`dalec-previous-image` build context:

```shell
docker buildx build -f spec.yml --target azlinux3/container \
  --build-context dalec-previous-image=docker-image://registry/myimage:1.0 \
  --no-cache-filter dalec.container \
  -t registry/myimage:1.0-1 --push .
```

Dalec then upgrades the packages installed in that image instead of starting
from scratch:

- Only packages with an available update (and any new dependencies they need)
  are downloaded and installed. Set `--build-arg DALEC_UPDATE_PACKAGES=openssl,openssl-libs`
  to limit the upgrade to specific packages.
- Your spec's packages are not built or reinstalled; they are kept as they are in
  the previous image, even when a newer version is published in the package
  repositories. To ship a new version of them at the same time, pass them as a
  [prebuilt package](targets.md#prebuilt-packages) build context.
- The image's `minimization_profile` and `post` symlinks are applied again
  after the update, like in a fresh build.
- The result is split into layers as usual, so every package that did not
  change produces the same layer as before and is not uploaded or downloaded
  again. Only the updated packages' layers and the final layer change.

This requires `layers: per-package`, and the previous image should have been
built with per-package layers too. It is supported for the Azure Linux,
AlmaLinux and Rocky Linux targets.

{{< callout type="info" >}}
Per-package layers are experimental. They are currently supported for RPM-based
targets without a custom base image (`image.bases`). Other targets produce a
single layer and print a warning.
{{< /callout >}}

### Base

The `base` field is used to specify the base image for the output container.

Example:

```yaml
image:
  base: docker.io/library/ubuntu:jammy
```


### Bases

As noted above, the `bases` field is used to specify the base image(s) for the
output container(s).
Multiple bases can be specified for the same target, but the target must support
it.
Currently the only built-in target that supports this is `windowscross/container`
where each base image specified is used to build for a different Windows version.


Example:

```yaml
image:
  bases:
    - rootfs:
        image:
          ref: mcr.microsoft.com/windows/nanoserver:1809
    - rootfs:
        image:
          ref: mcr.microsoft.com/windows/nanoserver:ltsc2025
    - rootfs:
        image:
          ref: mcr.microsoft.com/windows/nanoserver:ltsc2022
```

The data type allows specifying any kind of [source](sources.md) for the base image,
however currently only the `image` source is supported. Anything else will produce
an error.
Support for other source types may be added in the future.

### Post

The `post` field is used to specify post processing for the image.

The following fields are supported:

- `symlinks`: A list of symlinks to create in the image. The user and group can be set for each symlink as well.

Example:

```yaml
image:
  post:
    symlinks:
      /usr/bin/my-binary: # Where the symlink points to
        paths: # a list of symlinks that will point to /usr/bin/my-binary
         - /my-binary
        user: someuser
        group: somegroup
```

### Labels

The `labels` field is used to specify labels for the image.

Example:

```yaml
image:
  labels:
    com.example.label: example
```

#### Upstream source repository

Source-label inference is **disabled by default**. To opt in for container
outputs, pass the frontend input `dalec.image-source-label=true`. For example,
with BuildKit's `buildctl`:

```shell
buildctl build \
  --frontend gateway.v0 \
  --opt source=ghcr.io/project-dalec/dalec/frontend:latest \
  --local context=. \
  --local dockerfile=. \
  --opt filename=spec.yml \
  --opt target=bookworm/container \
  --opt dalec.image-source-label=true \
  --output type=oci,dest=image.tar
```

This is a frontend input, **not a build argument**. Setting
`--build-arg dalec.image-source-label=true` does not enable it (and is rejected
as an unknown argument unless declared in the spec). Docker CLI
build commands do not expose this input; use a client that supports arbitrary
frontend inputs, or configure `image.labels` explicitly instead.

When enabled, Dalec sets
`org.opencontainers.image.source` in the final image configuration's
`config.Labels` when the spec has exactly one top-level `sources.<name>.git`
source with a supported repository URL. This also works without an `image`
section and is applied after build-argument substitution for each output
platform, including Windows base-image variants.

RPM `depsonly` targets do not include the application, so they never infer its
source repository. Explicit global/target image labels still apply. When the
input is enabled, these targets also remove inherited source/revision labels
as described below; when absent or `false`, inherited provenance is preserved.

Only HTTP and HTTPS repository URLs are supported. SSH (including
`git@github.com:owner/repo.git`), `git://`, and local paths are not inferred,
even for well-known hosts. Credentials, query parameters, fragments and a
trailing slash are removed; a trailing `.git` is retained. Loopback/private IP
addresses are not inferred; use an explicit label for these cases.
Non-canonical numeric IPv4 hosts (such as `127.1`, `2130706433`,
or `0x7f000001`) are also excluded rather than relying on consumer-specific
address parsing.
Tilde-prefixed HTTP(S) path segments are supported, for example
`https://git.sr.ht/~sircmpwn/aerc`; they are not local home-directory paths.

For example, `sources.coredns.git.url: https://github.com/coredns/coredns.git`
produces `org.opencontainers.image.source: https://github.com/coredns/coredns.git`
when the input is enabled.
Other non-Git sources may coexist with that source. With multiple Git sources
(even copies of the same repository), no Git sources, or an unsupported URL,
Dalec does not infer a source label. It does not infer repositories from source
archives, `website`, generators, nested build contexts, or build/base images.

For ambiguous sources or archives, identify the component's upstream explicitly:

```yaml
image:
  labels:
    org.opencontainers.image.source: https://github.com/coredns/coredns
```

Explicit labels take precedence over inference, and target-specific labels take
precedence over global labels. An explicitly configured legacy
`org.label-schema.vcs-url` also disables inference, preserving its use by
existing consumers.

With the input enabled, a spec or target can opt out of inference by setting
the source label to an empty string:

```yaml
image:
  labels:
    org.opencontainers.image.source: ""
```

The empty label is retained in the output. **Only when the frontend input is
enabled**, Dalec removes inherited
`org.opencontainers.image.source`, `org.opencontainers.image.revision`,
`org.label-schema.vcs-url`, and `org.label-schema.vcs-ref` from the base image
before applying explicit spec/target labels, whether inference succeeds or not.
This prevents consumers from reporting the base image's repository or pairing
its commit with the component's repository. Other base labels are preserved.
No revision is inferred. Set it explicitly if needed. To suppress discovery,
also remove or empty any **explicit** legacy source label in your spec.

When the input is absent or `false`, Dalec neither infers a source label nor
removes inherited provenance. Explicit global/target labels still override
base labels as usual. In that mode, an empty OCI source label does not remove
an inherited legacy source label; consumers may still fall back to it.
The input accepts boolean values (`true`/`false` or `1`/`0`); invalid values,
including an explicitly empty input, fail container builds.

HTTP(S) alone does not imply that a repository is public. Enable this input
only when the repository URL is appropriate to disclose in the output image.

[Renovate's Docker datasource](https://docs.renovatebot.com/modules/datasource/docker/)
reads the source label on the latest stable image tag to discover `sourceUrl`.
This enables upstream changelog discovery, but does not guarantee release notes:
packaging-revision tags may need version mapping to upstream releases. Upstream
notes do not necessarily cover Dalec-specific patches, toolchain changes, or
revision-only CVE rebuilds. This does not add a `changelogUrl` label or change
package-only outputs.

### Env

The `env` field is used to specify environment variables for the image.

Example:

```yaml
image:
  env:
    - MY_ENV_VAR=my-value
```

### Entrypoint

The `entrypoint` field is used to specify the entrypoint for the image.

Example:

```yaml
image:
  entrypoint: /usr/bin/my-binary
```

### Cmd

The `cmd` field is used to specify the command for the image.

Example:

```yaml
image:
  cmd: /usr/bin/my-binary
```

### Workdir

The `workdir` field is used to specify the working directory for the image.

Example:

```yaml
image:
  workdir: /my-dir
```

### User

The `user` field is used to specify the user for the image.

Example:

```yaml
image:
  user: my-user:my-group
```

Alternatively

```yaml
image:
  user: my-user
```

You may also use uid/gid values:

```yaml
image:
  user: 1000:1000
```

Or just user:

```yaml
image:
  user: 1000
```

User and group names are not automatically created for you, so it must be in
the base OS to a username to work.

### Stop Signal

The `stop_signal` field is used to specify the stop signal for the image.
This is used by the container runtime to know what signal to use to gracefully
stop the container.

Example:

```yaml
image:
  stop_signal: SIGINT
```

### Volumes

The `volumes` field is used to specify volumes for the image.

Example:

```yaml
image:
  volumes:
    /some-path: {}
```

This is always a map of the path to create the volume at and an empty object.
