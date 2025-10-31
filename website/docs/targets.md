---
title: Targets
---

DALEC is designed to support building artifacts for a number of different
systems.
DALEC refers to these in the [spec](spec.md) as "targets".
When executing a build with Docker these targets can be specified with the
`--target=<target>` flag.

## Available Targets

DALEC includes a number of built-in targets that you can either use in your spec.

- `mariner2` - Azure Linux 2 (formerly CBL-Mariner)
- `azlinux3` - Azure Linux 3
- `bullseye` - Debian 11 (Bullseye) (v0.11)
- `bookworm` - Debian 12 (Bookworm) (v0.11)
- `bionic` - Ubuntu 18.04 (Bionic) (v0.11)
- `focal` - Ubuntu 20.04 (focal) (v0.11)
- `jammy` - Ubuntu 22.04 (jammy) (v0.9)
- `noble` - Ubuntu 24.04 (noble) (v0.11)
- `windowscross` - Cross compile from Ubuntu Jammy to Windows
- `almalinux9` - AlmaLinux 9 (v0.13)
- `almalinux8` - AlmaLinux 8 (v0.13)
- `rockylinux8` - Rocky Linux 8 (v0.13)
- `rockylinux9` - Rocky Linux 9 (v0.13)

When specifying a "target" to `docker build --target=<target>` DALEC treats
`<target>` as a route (much like an HTTP path) and each of the above mentioned
targets have subroutes you can specfiy as well, e.g. `jammy/deb` to have DALEC
build and output just the deb package. What subroutes are available depend on
the underlying target implementation.

To print a list of available build targets:

```shell
$ docker buildx build --call targets --build-arg BUILDKIT_SYNTAX=ghcr.io/project-dalec/dalec/frontend:latest - <<< "null"
```

import TargetsCLIOut from './examples/targets.md'

<details>
<summary>DALEC targets list output</summary>
<pre><TargetsCLIOut /></pre>
</details>

:::note
The above command is passing in a "null" value as the build spec and telling
buildkit to use the latest dalec version.
This output can change depending on version or spec you provide.
:::

To check the targets available for a specific spec you can just add `--call targets`
to your normal `docker build` command:

```shell
$ docker buildx build --call targets -f ./path/to/spec .
```

If the `--target=<val>` flag is set, the list of targets will be filtered based
on `<val>`.

Likewise if the spec file contains items in the `targets` section then the list
of available targets will be filtered to just the targets in the spec.

## Dependencies

Many components, such as package dependencies and base images, are specific to
a distro or a subset of distros. The dalec spec allows you to move these distro
specific things into a `target`.

Instead of specifying a package dependency at the root of the spec, you can specify it under a target.
This allows you to include different packages for different targets.

:::note
Please note that dependencies under a target will override dependencies at the root level.
:::

```yaml
targets:
  mariner2:
    dependencies:
      build:
        - golang
```

## Extensibility

Dalec can’t feasibly support every Linux distribution. Instead, it gives you the flexibility to specify a custom builder image for any target, directing the build process to that specified image.

This method allows for the use of a single spec file for all targets, employing one `#syntax=` directive to build the package for any specified target. It also permits the replacement of the default targets with custom builder configurations.

```yaml
targets:
  mariner2:
    frontend:
      image: docker.io/my/custom:mariner2
```

## Advanced Customization

### Worker images

In some cases you may need to have additional things installed in the worker
image that are not typically available in the base image. As an example, a
package dependency may not be available in the default package repositories.

You can have Dalec output an image with the target's worker image with
`<target>/worker>` build target, e.g. `--target=azlinux3/worker`. You can then
add any customizations and feed that back in via [source polices](#source-policies)
or [named build contexts](#named-build-contexts).

#### Step-by-step guide to build custom worker base images

**Step 1: Extract the base worker image**

First, extract the base worker image for your target:

```shell
# For azlinux3  
docker buildx build --target=azlinux3/worker --output=type=docker,name=dalec-azlinux3-base - <<< "null"

# For mariner2
docker buildx build --target=mariner2/worker --output=type=docker,name=dalec-mariner2-base - <<< "null"

# For jammy
docker buildx build --target=jammy/worker --output=type=docker,name=dalec-jammy-base - <<< "null"
```

**Step 2: Create a custom Dockerfile**

Create a Dockerfile that extends the base worker image:

```dockerfile
FROM dalec-azlinux3-base

# Install additional packages
RUN tdnf install -y \
    my-custom-package \
    another-dependency \
    && tdnf clean all

# Or for Debian/Ubuntu based targets:
# FROM dalec-jammy-base
# RUN apt-get update && apt-get install -y \
#     my-custom-package \
#     another-dependency \
#     && rm -rf /var/lib/apt/lists/*

# Add custom repositories or files
COPY custom-repo.repo /etc/yum.repos.d/
COPY custom-config.conf /etc/myapp/

# Set environment variables if needed
ENV CUSTOM_VAR=value
```

**Step 3: Build your custom worker image**

```shell
docker build -t my-custom-worker:azlinux3 .
```

**Step 4: Use the custom worker image**

You can use your custom worker image in two ways:

**Option A: Named Build Context (Recommended)**
```shell
docker buildx build \
    --build-context dalec-azlinux3-worker=my-custom-worker:azlinux3 \
    --target=azlinux3/rpm \
    -f myspec.yml .
```

**Option B: Source Policy (Advanced)**
```shell
# Create a source policy file
cat > source-policy.json << 'EOF'
{
  "rules": [
    {
      "action": "CONVERT",
      "selector": {
        "identifier": "mcr.microsoft.com/azurelinux/base/core:3.0"
      },
      "updates": {
        "identifier": "my-custom-worker:azlinux3"
      }
    }
  ]
}
EOF

# Use with buildx via environment variable
EXPERIMENTAL_BUILDKIT_SOURCE_POLICY=source-policy.json docker buildx build \
    --target=azlinux3/rpm \
    -f myspec.yml .
```

#### Complete Example

Here's a complete example that demonstrates installing tools that can't be easily added via dalec spec:

**custom-worker.Dockerfile:**
```dockerfile
# syntax=docker/dockerfile:1
FROM scratch AS base-worker

FROM base-worker AS final
# Install a custom Rust toolchain from source (specific version not available in repos)
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain 1.75.0 \
    && . ~/.cargo/env \
    && rustup target add x86_64-unknown-linux-musl

# Set up environment for the custom toolchain
ENV PATH="/root/.cargo/bin:$PATH"
ENV CARGO_TARGET_X86_64_UNKNOWN_LINUX_MUSL_LINKER="x86_64-linux-musl-gcc"

# Install a proprietary tool from a custom source (example)
COPY proprietary-tool.tar.gz /tmp/
RUN cd /tmp && tar -xzf proprietary-tool.tar.gz \
    && cp proprietary-tool/bin/* /usr/local/bin/ \
    && rm -rf /tmp/proprietary-tool*

# Configure custom CA certificates for corporate environment
COPY custom-ca-certs.pem /usr/local/share/ca-certificates/custom.crt
RUN update-ca-certificates
```

**Build and use:**
```shell
# 1. Extract base worker
docker buildx build --target=azlinux3/worker --output=type=docker,name=dalec-azlinux3-base - <<< "null"

# 2. Build custom worker
docker build --build-context base-worker=dalec-azlinux3-base -t my-custom-worker:azlinux3 -f custom-worker.Dockerfile .

# 3. Use custom worker in dalec build
docker buildx build \
    --build-context dalec-azlinux3-worker=my-custom-worker:azlinux3 \
    --target=azlinux3/rpm \
    -f myspec.yml .
```

#### Troubleshooting Custom Worker Images

**Common Issues:**

1. **Worker image extraction fails:**
   - Ensure you have the latest dalec frontend: `docker pull ghcr.io/azure/dalec/frontend:latest`
   - Use `null` as the spec when extracting worker images

2. **Custom packages not found:**
   - Verify your custom repositories are properly configured
   - Check that package cache is updated in your Dockerfile
   - For RPM targets: run `tdnf clean all` after repository changes
   - For DEB targets: run `apt-get update` after repository changes  

3. **Build context not found:**
   - Ensure your custom worker image is available locally or in a registry
   - Use `docker images` to verify the image was built successfully
   - Check the exact spelling of context names (they are case-sensitive)

4. **Permission issues:**
   - Custom worker images should maintain the same user/permissions as base workers
   - Avoid changing the working directory unless necessary

**Best Practices:**

- Use multi-stage Dockerfiles to keep custom workers clean
- Always clean package caches to reduce image size
- Test custom workers with simple builds before complex ones
- Document any custom repositories or dependencies added
- Use specific image tags rather than `latest` for reproducible builds

#### Advanced Integration Examples

**Using Docker Buildx Bake**

Create a `docker-bake.hcl` file that chains worker and package builds:

```hcl
# docker-bake.hcl
target "worker" {
    target = "azlinux3/worker" 
    dockerfile = "/path/to/spec.yml"
    args = {
        "BUILDKIT_SYNTAX" = "ghcr.io/azure/dalec/frontend:latest"
    }
}

target "enhanced-worker" {
    contexts = {
        "base-worker" = "target:worker"
    }
    dockerfile-inline = <<EOT
        FROM base-worker
        RUN tdnf install -y \
            custom-package \
            development-tools \
            && tdnf clean all
        
        ENV CUSTOM_BUILD_FLAGS="-O2 -g"
    EOT
}

target "build-package" {
    dockerfile = "/path/to/spec.yml"
    args = {
        "BUILDKIT_SYNTAX" = "ghcr.io/azure/dalec/frontend:latest"
    }
    contexts = {
        "dalec-azlinux3-worker" = "target:enhanced-worker"
    }
    target = "azlinux3/rpm"
    output = ["_output"]
}
```

Then build everything in sequence:

```shell
# Build all targets in dependency order
docker buildx bake build-package

# Or build specific targets
docker buildx bake worker enhanced-worker
docker buildx bake build-package
```


### Source Policies

`docker buildx build` has experimental support for providing a
[source policy](https://docs.docker.com/build/building/variables/#experimental_buildkit_source_policy)
which updates the base image ref used to create the worker image. This method
will update any and all references to the matched image used for any part of
the build. It also requires knowing the image(s) that are used ahead of time and
creating the right set of match rules and potentially having to update this in
the future if the worker image refs in Dalec change.

A finer grained approach is to use [named build contexts](#named-build-contexts).

### Named Build Contexts

`docker buildx build` has a flag called `--build-context`
([doc](https://docs.docker.com/reference/cli/docker/buildx/build/#build-context))
which allows you to provide additional build contexts apart from the main build
context in the form of `<name>=<ref>`. See the prior linked documentation for
what can go into `<ref>`.

This is the **recommended approach** for using custom worker images. For each target, 
Dalec looks for a named context called either:

1. The actual base image used internally for the target
2. A build context named `dalec-<target>-worker`

If option 1 is provided, then option 2 is ignored.

**Named Build Context Examples:**

```shell
# Using named build context approach (recommended)
docker buildx build \
    --build-context dalec-mariner2-worker=my-custom-worker:mariner2 \
    --target=mariner2/rpm \
    -f myspec.yml .

# Using base image replacement approach  
docker buildx build \
    --build-context mcr.microsoft.com/azurelinux/base/core:3.0=my-custom-worker:azlinux3 \
    --target=azlinux3/rpm \
    -f myspec.yml .
```

**Supported targets and their context names:**

| Target | Base Image | Named Context |
|--------|------------|---------------|
| `mariner2` | `mcr.microsoft.com/cbl-mariner/base/core:2.0` | `dalec-mariner2-worker` |
| `azlinux3` | `mcr.microsoft.com/azurelinux/base/core:3.0` | `dalec-azlinux3-worker` |
| `jammy` | `docker.io/library/ubuntu:jammy` | `dalec-jammy-worker` |
| `noble` | `docker.io/library/ubuntu:noble` | `dalec-noble-worker` |
| `focal` | `docker.io/library/ubuntu:focal` | `dalec-focal-worker` |
| `bionic` | `docker.io/library/ubuntu:bionic` | `dalec-bionic-worker` |
| `bookworm` | `docker.io/library/debian:bookworm` | `dalec-bookworm-worker` |
| `bullseye` | `docker.io/library/debian:bullseye` | `dalec-bullseye-worker` |
| `windowscross` | `docker.io/library/ubuntu:jammy` | `dalec-windowscross-worker` |

### Target Defined Artifacts

There are some situations where you may want to have multiple builds and for those different
targets they may require different binaries to exist that are not globally applicable to all
of the builds. For example, `windowscross` may require specific artifacts (binaries, docs,
config files, etc.) that are not relevant to `azlinux3`, and vice versa.

To address this you can define artifacts per target. Target-defined artifacts will override
global (spec-defined) artifacts if there is a conflict. However, if a target does not define
an artifact, it will inherit artifacts from the global spec.

Here is an example:

```yaml
targets:
  windowscross:
    artifacts:
      binaries:
        bin/windows-cross.exe:
          subpath: ""
          mode: 0o755
  azlinux3:
    artifacts:
      binaries:
        bin/linux-binary:
          subpath: ""
          permissions: 0o755
```

For more details on how Artifacts are structured and configured, see the [Artifacts](artifacts.md) documentation.

### Target defined package metadata

`conflicts`, `replaces`, and `provides` can be defined at the target level in addition to the [globalspec level](spec.md#additional-metadata).
This allows you to define package metadata that is specific to a target.

```yaml
targets:
  mariner2:
    package:
      conflicts:
        - "foo"
        - "bar"
      replaces:
        - foo"
      provides:
        - "qux"
```

## Special considerations

### Windows

When using the `windowscross` target you will need to make sure that binaries use the `.exe` extension.

```yaml
build:
  steps:
    - command: |
        go build -o _output/bin/dalec_example.exe
```

You can use the built-in `TARGETOS` build-arg to determine if the build is targeting Windows or not.
Alternatively you can use the built-in `DALEC_TARGET` build-arg to determine the target being built.

```yaml
build:
  env:
    TARGETOS: ${TARGETOS}
  steps:
    - command: |
        if [ "$TARGETOS" = "windows" ]; then
          go build -o _output/bin/dalec_example.exe
        else
          go build -o _output/bin/dalec_example
        fi
```

```yaml
build:
  env:
    DALEC_TARGET: ${DALEC_TARGET}
  steps:
    - command: |
        if [ "$DALEC_TARGET" = "windowscross" ]; then
          go build -o _output/bin/dalec_example.exe
        else
          go build -o _output/bin/dalec_example
        fi
```

Since `windowscross` is intended for cross-compilation, the environment has the
following env vars set by default:

- `GOOS=windows` - ensures that by default `go build` produces a Windows binary

This can be overridden in your spec by either setting them in the `env` section
or in the actual build step script, which may be necessary if you need to
build tooling or other things first.

```yaml
build:
  env:
    GOOS: linux
  steps:
    - command: |
        go build -o _output/bin/dalec_example
```

```yaml
build:
  steps:
    - command: |
        GOOS=linux go build -o _output/bin/dalec_example
```

## Tips

### Overriding default debian rules
Debian-based distros currently use dpkg-buildpackage to produce the deb package.
This brings a lot of functionality with it, sometimes perhaps too much.
dpkg-buildpackage, under the covers, is driven by a `rules` at `debian/rules`.
Dalec generates this file for the rules it needs to inject to make the build behave per the dalec spec.

Some of the rules followed by dpkg-buildpackage, most in fact, are implicit and not actually in `debian/rules`
explicitly.
You can override some of these rules from within the build, though not all.
See the [debian rules file docs](https://www.debian.org/doc/manuals/maint-guide/dreq.en.html#rules)
for details on these rules and how to override them.

Example override from a dalec spec:

```yaml
build:
  steps:
    - command: |
        # If the debian directory doesn't exist, then there is nothing to do
        [ -d debian ] || exit 0

        # Add a makefile rule to override how dpkg-shlibdeps is executed
        echo "override_dh_shlibdeps:" >> debian/rules
        # Ignore errors from dh_shlibdeps
        echo "	dh_shlibdeps --dpkg-shlibdeps-params=--ignore-missing-info" >> debian/rules
```

Note that at the point your build steps are run some of the rules have already been applied.
This is considered a "break glass" way to work around issues when building on Debian-based distributions.
Overriding some rules, such as `dh_install`, will also interfere with dalec functionality.

In the future dalec may stop using dpkg-buildpackage and these rules would not be applicable anymore.

