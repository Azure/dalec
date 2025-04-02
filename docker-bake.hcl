group "default" {
    targets = ["frontend"]
}

group "test" {
    targets = ["test-fixture", "runc-test", "test-deps-only"]
}

variable "FRONTEND_REF" {
    default = "local/dalec/frontend"
}

// This is used to forcibly diff/merge ops in the frontend for testing purposes.
// Set to "1" to disable diff/merge ops.
variable "DALEC_DISABLE_DIFF_MERGE" {
    default = "0"
}

target "frontend" {
    // uses default Dockerfile
    target = "frontend"
    tags = [FRONTEND_REF]
}


target "spec" {
    dockerfile = "test/fixtures/yarn.yml"
    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    # target = "mariner2/rpm/debug/sources"
    target = "mariner2/rpm"
    output = ["_output2"]
    tags = ["local/dalec/spec:az2-container"]
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

# When running with docker <= v24, the nested build will fail with the following error:
#   missing provenance for 61xav4v751u8cuhj3ydxmgoob
# This is due to a bug in buildkit. Its already been fixed but not yet backported to moby v24.
# This should be removed once the bug is fixed.
variable "DALEC_DISABLE_NESTED" {
    default = "0"
}

target "test-fixture" {
    name = "test-fixture-${f}"
    matrix = {
        f = DALEC_DISABLE_NESTED == "1" ? (
            ["http-src", "frontend", "local-context", "cmd-src-ref"]
        ) : (
            ["http-src", "frontend", "local-context", "cmd-src-ref", "nested"]
        )
        tgt = ["mariner2/container"]
    }
    dockerfile = "test/fixtures/${f}.yml"

    args = {
        "BUILDKIT_SYNTAX" = "dalec_frontend"
        "DALEC_DISABLE_DIFF_MERGE" = DALEC_DISABLE_DIFF_MERGE
    }
    contexts = {
        "dalec_frontend" = "target:frontend"
    }
    target = tgt
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
        distro = ["mariner2"]
        f = ["go-md2man-2"]
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
