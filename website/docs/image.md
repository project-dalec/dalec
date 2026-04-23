---
title: Image
---

Image is a field in the DALEC spec that allows you to customize certain aspects
of the produced image. The image field is an object with the following properties:

- `base`: The image ref to use as the base for the output container. [Deprecated: use `bases` instead] [base section](#base)
- `bases`: The list of base images to use as the base for the output container(s). [bases section](#bases)
- `post`: The post processing for the image, such as symlinks. [post section](#post)
- `labels`: The labels for the image. This is an optional field. [labels section](#labels)
- `env`: The environment variables for the image. This is an optional field. [env section](#env)
- `entrypoint`: The entrypoint for the image. This is an optional field. [entrypoint section](#entrypoint)
- `cmd`: The command for the image. This is an optional field. [cmd section](#cmd)
- `workdir`: The working directory for the image. This is an optional field. [workdir section](#workdir)
- `user`: The user for the image. This is an optional field. [user section](#user)
- `stop_signal`: The stop signal for the image. This is an optional field. [stop signal section](#stop-signal)
- `volumes`: The volumes for the image. This is an optional field. [volumes section](#volumes)


:::note
The `base` field is deprecated. Use the `bases` field instead.

For `bases`, the requested build target may not support multiple base images.
In this cases the target will produce an error.

Currently only `windowscross/container` supports multiple base images for the
purpose of building for multiple windows versions.
If multiple bases are provided with the same `os.version` value in the image
platform, this may produce an error or at least unexpected results since images
are keyed on the image platform metadata.
:::

With the exception of `base`, `bases`, and `post`, these fields are all used to
merge with the configured (or default) base image(s).

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

