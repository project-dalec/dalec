group "default" {
    targets = ["frontend"]
}

group "test" {
    targets = ["runc-test", "test-deps-only"]
}

variable "FRONTEND_REF" {
    default = "local/dalec/frontend"
}

// This is used to forcibly diff/merge ops in the frontend for testing purposes.
// Set to "1" to disable diff/merge ops.
variable "DALEC_DISABLE_DIFF_MERGE" {
    default = "0"
}

variable "WORKER_TARGET" {
    default = "must set WORKER_TARGET variable to the target to build for the worker"
}


target "frontend" {
    // uses default Dockerfile
    target = "frontend"
    tags = [FRONTEND_REF]
}


# Run linters
# Note: CI is using the github actions golangci-lint action which automatically sets up caching for us rather than using this bake target
# If you change this, please also change the github action
target "lint" {
    context = "."
    dockerfile-inline = <<EOT
    FROM golangci/golangci-lint:v2.1.6
    WORKDIR /build
    RUN \
        --mount=type=cache,target=/go/pkg/mod \
        --mount=type=cache,target=/root/.cache,id=golangci-lint \
        --mount=type=bind,source=.,target=/build \
        golangci-lint run -v
    EOT
}

variable "RUNC_COMMIT" {
    default = "v1.1.9"
}

variable "RUNC_VERSION" {
    default = "1.1.9"
}

variable "RUNC_REVISION" {
    default = "1"
}

target "maps-dep" {
    name = "maps-dep-${replace(tgt, "/", "-")}"
    dockerfile = "test/fixtures/sourcemap-test-bad-builddep.yml"
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    matrix = {
        tgt = ["azlinux3/rpm", "mariner2/rpm", "jammy/deb", "noble/deb", "bookworm/deb", "bullseye/deb"]
    }
    target = tgt
}

target "maps-buildstep" {
    name = "maps-buildstep-${replace(tgt, "/", "-")}"
    dockerfile = "test/fixtures/sourcemap-test-bad-buildstep.yml"
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    matrix = {
        tgt = ["azlinux3/rpm", "mariner2/rpm", "jammy/deb", "noble/deb", "bookworm/deb", "bullseye/deb"]
    }
    target = tgt
}

target "maps-test" {
    name = "maps-test-${replace(tgt, "/", "-")}"
    dockerfile = "test/fixtures/sourcemap-test-bad-test.yml"
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    matrix = {
        tgt = ["azlinux3/rpm", "mariner2/rpm", "jammy/deb", "noble/deb", "bookworm/deb", "bullseye/deb"]
    }
    target = tgt
}

target "maps-source" {
    name = "maps-source-${replace(tgt, "/", "-")}"
    dockerfile = "test/fixtures/sourcemap-test-bad-sources.yml"
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    matrix = {
        tgt = ["azlinux3/rpm", "mariner2/rpm", "jammy/deb", "noble/deb", "bookworm/deb", "bullseye/deb"]
    }
    target = tgt
}

group "maps-all" {
    targets = ["maps", "maps-buildstep", "maps-test"]
}

target "runc-azlinux" {
    name = "runc-${distro}-${replace(tgt, "/", "-")}"
    dockerfile = "test/fixtures/moby-runc.yml"
    args = {
        "RUNC_COMMIT" = RUNC_COMMIT
        "VERSION" = RUNC_VERSION
        "REVISION" = RUNC_REVISION
        "BUILDKIT_SYNTAX" = "dalec_frontend"
        "DALEC_DISABLE_DIFF_MERGE" = DALEC_DISABLE_DIFF_MERGE
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    matrix = {
        distro = ["mariner2", "azlinux3"]
        tgt = ["rpm", "container", "rpm/spec"]
    }
    target = "mariner2/${tgt}"
    // only tag the container target
    tags = tgt == "container" ? ["runc:mariner2"] : []
    // only output non-container targets to the fs
    output = tgt != "container" ? ["_output"] : []
}

target "runc-jammy" {
    name = "runc-jammy-${replace(tgt, "/", "-")}"
    dockerfile = "test/fixtures/moby-runc.yml"
    args = {
        "RUNC_COMMIT" = RUNC_COMMIT
        "VERSION" = RUNC_VERSION
        "REVISION" = RUNC_REVISION
        "BUILDKIT_SYNTAX" = "dalec_frontend"
        "DALEC_DISABLE_DIFF_MERGE" = DALEC_DISABLE_DIFF_MERGE
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    matrix = {
        tgt = ["deb", "container"]
    }
    target = tgt == "container" ? "jammy/testing/${tgt}" : "jammy/${tgt}"
    // only tag the container target
    tags = tgt == "container" ? ["runc:jammy"] : []
    // only output non-container targets to the fs
    output = tgt != "container" ? ["_output"] : []
}

target "runc-test" {
    name = "runc-test-${distro}"
    matrix = {
        distro =["mariner2", "azlinux3", "jammy"]
    }
    contexts = {
        "dalec-runc-img" = "target:runc-${distro}-container"
    }
    dockerfile-inline = <<EOT
    FROM dalec-runc-img
    EOT
}

variable "BUILD_SPEC" {
    default = "\"ERROR: must set BUILD_SPEC variable to the path to the build spec file\""
}

target "build" {
    name = "build-${distro}-${tgt}"
    matrix = {
        distro = ["mariner2"]
        tgt = ["rpm", "container"]
    }
    dockerfile = BUILD_SPEC
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    target = "${distro}/${tgt}"
    // only tag the container target
    tags = tgt == "container" ? ["build:${distro}"] : []
    // only output non-container targets to the fs
    output = tgt != "container" ? ["_output"] : []
}

target "examples" {
    name = "examples-${f}"
    matrix = {
        distro = ["azlinux3"]
        f = ["go-md2man"]
    }
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    target = "${distro}/container"
    dockerfile = "docs/examples/${f}.yml"
    tags = ["local/dalec/examples/${f}:${distro}"]
}

target "go-stuff" {
    name = "examples-${f}-${tgt}"
    matrix = {
        distro = ["azlinux3"]
        f = ["simple-go-require"]
        tgt = ["sources", "patched-sources"]
    }
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    // target = "${distro}/container"
    // target = "debug/sources"
    target = "debug/${tgt}"
    dockerfile = "test/fixtures/${f}.yml"
    // tags = ["local/dralec/examples/${f}:${distro}"]
    output = [ "_src_${tgt}" ]

}
target "go-azl3-container-stuff" {
    name = "examples-${f}-${tgt}"
    matrix = {
        distro = ["azlinux3"]
        f = ["simple-go-require"]
        tgt = ["rpm"]
    }
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    target = "${distro}/${tgt}"
    dockerfile = "test/fixtures/${f}.yml"
    tags = ["local/dalec/examples/${f}:${distro}"]
    output = [ "_src_${tgt}" ]

}
target "godebugmod" {
    name = "godebugmod-examples-${replace(tgt, "/", "-")}-${f}"
    matrix = {
        f = ["simple-go-require"]
        tgt = ["azlinux3/rpm/debug/sources", "jammy/deb"]
    }
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    target = tgt
    dockerfile = "test/fixtures/${f}.yml"
    output = [ "_src_debug_gomods" ]
}

target "deps-only" {
    name = "deps-only-${distro}"
    matrix = {
        distro = ["mariner2"]
    }
    dockerfile-inline = <<EOT
dependencies:
    runtime:
        patch: {}
        bash: {}
    EOT
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    target = "${distro}/container/depsonly"
    tags = ["local/dalec/deps-only:${distro}"]
}

target "test-deps-only" {
    dockerfile-inline = <<EOT
    FROM deps-only-context
    # Make sure the deps-only target has the runtime dependencies we expect and not, for instance, "rpm"
    RUN command -v bash
    RUN command -v patch
    RUN if command -v rpm; then echo should be a distroless image but rpm binary is installed; exit 1; fi
    EOT

    contexts = {
        "deps-only-context" = "target:deps-only-mariner2"
    }
}


variable "CI_FRONTEND_CACHE_SCOPE" {
    default = "dalec/frontend/ci"
}

target "frontend-ci" {
    inherits = ["frontend"]
    output = ["type=registry"]
}

target "frontend-ci-full" {
    inherits = ["frontend-ci"]
    platforms = ["linux/amd64", "linux/arm64"]
}

target "worker" {
    target = WORKER_TARGET
    dockerfile-inline = "{}"
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
}
