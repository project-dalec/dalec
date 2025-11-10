# Signing Packages

:::note
Available with Dalec release `v0.3.0` and later.
:::

Packages can be automatically signed using Dalec. To do this, you will
need to provide a signing frontend. There is an example in the test
code `test/signer/main.go`. Once that signing image has been built and
tagged, you need to tell dalec which signing image to use. You can do this
in a few different ways:

1. Provide the signing config in the spec, under
   `targets.[target].package_config.signer` or `.package_config.signer`
1. Provide the signing config via the `DALEC_SIGNING_CONFIG_PATH` build arg.
   Otherwise, the signing config will be sought in the spec.
1. The path name will be sought in the main (local) context, unless the build
   arg `DALEC_SIGNING_CONFIG_CONTEXT_NAME` is provided, in which case the dalec
   will seek the config in the context with the provided name *and at the
   provided path*.

Please note the order of precedence of the above-mentioned build args:
1. Configs provided via a build context (whether local or custom) will always
   take precedence over configs provided in the spec.
1. The `DALEC_SKIP_SIGNING` build-arg takes precedence over all. If it is
   truthy, no signing will take place.

In most cases, you will add it to the spec directly to trigger the signing
operation:

```yaml
name: my-package
targets: # Distro specific build requirements
  mariner2:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
```

At this time, these targets can leverage package signing:

- `windowscross/zip`
- `mariner2/rpm`
- `windowscross/container`

For container targets, only the artifacts within the container get signed.

This will send the artifacts (`.rpm`, `.deb`, or `.exe`) to the
signing frontend as the build context.

Once a `signer` section has been added to the spec, signing will be automatic.
In order to disable signing when building specs that have a `signer` section,
use the build arg `DALEC_SKIP_SIGNING=1`.

The contract between dalec and the signing image is:

1. The signing image will contain both the signing frontend, and any
additional tooling necessary to carry out the signing operation.
1. The `llb.State` corresponding the artifacts to be signed will be
provided as the build context.
1. Dalec will provide the value of `dalec.target` to the frontend as a
`FrontendOpt`. In the above example, this will be `mariner2`.
1. The response from the frontend will contain an `llb.State` that is
identical to the input `llb.State` in every way *except* that the
desired artifacts will be signed.

A signer can also be configured at the root of the spec if there is no target
specific customization required or you are only building for one target.

Example:

```yaml
name: my-package
package_config:
  signer:
    image: "ref/to/signing:image"
    cmdline: "/signer"
```

## Responsibilities of the Signer

Dalec sends the output of the package build as the build context to the
signer.
The signer should provide in the response an `llb.State` with the signed
artifacts as the result.
Dalec then merges the signed artifacts back into the original package build
result.

## Build Time customization

Signing artifacts may require passing through build-time customizations.
This can be done through 3 mechanisms:

1. [Secrets](#secrets)
2. [Named contexts](#named-contexts)
3. [Build arguments](#build-arguments)

With all methods of build-time customization, the signer needs to be coded
such that it is going to consume the customizations that are passed in, as such
all such customizations are signer specific.

### Secrets

Secrets are passed through from the client (such as the docker CLI or buildx).
These secrets are always available to the signer.
see Docker's [secrets](https://docs.docker.com/build/building/secrets/)
documentation for more details on on how secrets can be passed into a build
using the docker CLI.

*Note*: The docker documentation is using Dockerfiles in their examples which
are irrelevant for Dalec signing, however the CLI examples for how to pass in
those secrets is useful.

No changes to the spec yaml are required to use secrets with a signer, except
that the signer itself needs to be setup to consume the secret(s).

### Named Contexts

Named contexts are passed into the build by the client. All named contexts are
available to the signer.

A named context is just like a regular
[build context](https://docs.docker.com/build/building/context/) except that it
is given a custom name where as the regular build context is specifically named
`context`. In the scope of Dalec signing, the regular build context is the
packages that Dalec is giving to the signer to sign.
A named context can be used to provide extra data or configuration to the signer.

Example usage with Docker:

```console
$ docker build --build-context my-signing-config=./signing-config-dir ...
```

Here `my-singing-config` is the name you want to give to the context which the
signer may use to pull in the context. The `./signing-config-dir` is the data
being given as the context, in this case a local directory. This could be a
directory, a git ref, an HTTP url, etc. See the linked docker build context
documentation above for more details on what can be specified.

Multiple named contexts may be provided.

No changes to the spec yaml are required to use named contexts with a signer,
except that the signer itself needs to be setup to consume the named
context(s).

### Build Arguments

Build arguments are key/value pairs that can be supplied in the yaml spec which
will be forwarded to the signer.

Taking the original example above we can add build by adding an `args` with
a string-to-string mapping like so:

```yaml
targets: # Distro specific build requirements
  mariner2:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
        args:
            SOME_KEY: SOME_VALUE
            SOME_OTHER_KEY: SOME_OTHER_VALUE
```

The values of these arguments can also be taken from the client using variable
substitution like in other parts of the spec.
To use variable substitution, the args must be declared at the root of the spec:

```yaml
args:
  SOME_SIGNING_ARG: ""
  SOME_OTHER_SIGNING_ARG: "default_value"

targets: # Distro specific build requirements
  mariner2:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
        args:
            SOME_KEY: "${SOME_SIGNING_ARG}"
            SOME_OTHER_KEY: "${SOME_OTHER_SIGNING_ARG}"
```
