# Caches

In addition to the standard step-level caching that buildkit provides, you can
also configure incremental caching that persists across builds.
This is similar to Dockerfiles `--mount=type=cache`.

You can provide multiple cache configurations:

```yaml
caches:
  - dir:
      key: my_key
      dest: /my/cache/dir
      sharing: shared
  - dir:
      key: my_other_key
      dest: /my/other/cache/dir
      sharing: private
  - gobuild:
  - bazel:
```

## Dir Cache

Dir caches are just generic directories where you choose the cache key and sharing mode.

```yaml
caches:
  - dir:
      key: my_key
      dest: /my/cache/dir
      sharing: shared
```

Supported sharing modes are:
- `shared`: The cache is shared between all builds.
- `private`: The cache is private to the build.
- `locked`: The cache is locked to the build. This is useful for caching directories that are not thread-safe.

By default, dalec will namespace these directories with a key tied to the OS and
CPU architecture which is prepended to the key you provide. This is to help
prevent common issues one would see for specific use-cases such as storing
incremental compiler caches.
You can disable this behavior by setting the `no_auto_namespace` option to `true`.

```yaml
caches:
  - dir:
      key: my_key
      dest: /my/cache/dir
      sharing: shared
      no_auto_namespace: true
```

This will disable the automatic namespacing and use the key you provide as-is.


## Gobuild Cache

The gobuild cache is a special type of cache that is used to cache the results of
the `go build` command.
This is useful for caching the results of the build, such as the binary or
libraries.

```yaml
caches:
  - gobuild:
```

Go build caches are always in `shared` mode and are always namespaced with the OS and CPU architecture.

An optional `scope` can be provided which is added to the generated cache key.
This is intended for internal testing purposes, however may be useful for other
use-cases as well.

```yaml
caches:
  - gobuild:
      scope: my_scope
```

Finally, when go is detected in the build environment dalec will automatically
create a gobuild cache for you. This can be disabled by setting the `disabled`
option to `true` in the cache definition.

```yaml
caches:
  - gobuild:
      disabled: true
```

## Bazel Cache

The bazel cache is a special type of cache that is used to cache the results of
the `bazel build` command.

```yaml
caches:
  - bazel:
```

Bazel caches are always in `shared` mode and are always namespaced with the OS and CPU architecture.
This relies on setting the `--disk_cache` flag on `bazel build` by adding it to the *system* bazelrc file
when dalec sets up the build environment.
Dalec does not check if this is overwritten in the user or project bazelrc files.
If this conflicts with your project, you may need to manually manage the bazel cache with a [cache dir](#dir-cache).

An optional `scope` can be provided which is added to the generated cache key.
This is intended for internal testing purposes, however may be useful for other
use-cases as well.

```yaml
caches:
  - bazel:
      scope: my_scope
```

### Remote Cache

Bazel supports remote caching. When bazel caching is enabled in the spec dalec will look for a socket ID, `bazel-default`,
which is provided by a buildkit client and forward it into the build environment adding the appropriate configuration to
bazelrc.

:::note
If using docker buildx, you will need to set `--ssh socket=<path>,raw=true`, which requires buildx version 0.25.0 or later.

DALEC provides a (go) library to help with this in the `sessionutil/socketprovider` package that you can use with a custom buildkit client integration.
These are generic and not specific to dalec or bazel caching, so they must be configured based on your needs.
:::

1. `socketprovider.SocketProxy` - This implements the required interface to pass sockets to buildkit.
2. `socketprovider.Mux` - Routes requests for sockets to different backends,
    this is needed if you'll want to also expose multiple socket forwarding agents to the build -- for example, a standard SSH agent and a bazel remote cache connection. This acts sort of like an http.ServeMux but for buildkit's
    socket forwarding implementation, where a request comes in with a socket ID and
    the mux routes it to the appropriate backend based on the ID, which is up to you
    to configure how you want those routes to be handled.

These implement the
[session.Attachable](https://pkg.go.dev/github.com/moby/buildkit/session#Attachable) interface and need to be provided to the
[SolveOpt](https://pkg.go.dev/github.com/moby/buildkit/client#SolveOpt) in the `Session` field when starting a build.

The way this works is the buildkit client is expected to provide a set of GRPC API's tunnelled through a "session".
The buildkit solver will call those API's for a number of things, including fetching files, authentication, and in this case
forwarding sockets.
The API's for forwarding sockets are designed around forwarding SSH agents, but the buildkit solver doesn't care about SSH
or the agent protocol at all.
The docker CLI, however, is expecting to only provide SSH agent forwarding and is not able to be used with generic proxies like
is provided in these libraries.
Any buildkit server should work with this, it is just that the buildkit client requires special configuration to do this.

[bazel-remote](https://github.com/buchgr/bazel-remote) is an example of a remote bazel caching server, but technically any
implementation of the [bazel remote caching protocol](https://bazel.build/remote/caching) can be used.