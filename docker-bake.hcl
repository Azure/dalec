group "default" {
    targets = ["frontend"]
}

group "test" {
    targets = ["test-runc", "test-fixture"]
}

variable "FRONTEND_REF" {
    // Buildkit always checks the registry for the frontend image.
    // AFAIK there is no way to tell it not to.
    // Even if we have the image locally it will still check the registry and use that instead.
    // As such we need to use a local only ref to ensure we always use the local image when testing things.
    //
    // We'll use this var to set the `BUILDKIT_SYNTAX` var in the builds that consume the frontend which will
    // cause buildkit to use the local image.
    default = "local/dalec/frontend"
}

target "frontend" {
    target = "frontend"
    tags = [FRONTEND_REF]
}

target "mariner2-toolchain" {
    dockerfile = "./frontend/mariner2/Dockerfile"
    target = "toolchain"
    tags = ["ghcr.io/azure/dalec/mariner2/toolchain:latest"]
    cache-from = ["type=registry,ref=ghcr.io/azure/dalec/mariner2/toolchain:cache"]
}

# Run linters
# Note: CI is using the github actions golangci-lint action which automatically sets up caching for us rather than using this bake target
# If you change this, please also change the github action
target "lint" {
    context = "."
    dockerfile-inline = <<EOT
    FROM golangci/golangci-lint:v1.54
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

target "runc" {
    name = "runc-${distro}-${tgt}"
    dockerfile = "test/fixtures/moby-runc.yml"
    args = {
        "RUNC_COMMIT" = RUNC_COMMIT
        "VERSION" = RUNC_VERSION
        "REVISION" = RUNC_REVISION
        "BUILDKIT_SYNTAX" = FRONTEND_REF
    }
    matrix = {
        distro = ["mariner2"]
        tgt = ["rpm", "container", "toolkitroot"]
    }
    contexts = {
        "mariner2-toolchain" = "target:mariner2-toolchain"
    }
    target = "${distro}/${tgt}"
    // only tag the container target
    tags = tgt == "container" ? ["runc:${distro}"] : []
    // only output non-container targets to the fs
    output = tgt != "container" ? ["_output"] : []

    cache-from = ["type=gha,scope=dalec/${distro}/${tgt}"]
    cache-to = ["type=gha,scope=dalec/${distro}/${tgt},mode=max"]
}

target "test-runc" {
    name = "test-runc-${distro}"
    matrix = {
        distro = ["mariner2"]
    }
    contexts = {
        "dalec-runc-img" = "target:runc-${distro}-container"
    }

    dockerfile-inline = <<EOT
    FROM dalec-runc-img
    RUN [ -f /usr/bin/runc ]
    RUN for i in /usr/share/man/man8/runc-*; do [ -f "$i" ]; done
    # TODO: The spec is not currently setting the revision in the runc version
    RUN runc --version | tee /dev/stderr | grep "runc version ${replace(RUNC_VERSION, ".", "\\.")}"

    # Make sure this is setup correctly as a distroless image
    RUN [ -f /var/lib/rpmmanifest/container-manifest-1 ] && grep -q "moby-runc-${RUNC_VERSION}" /var/lib/rpmmanifest/container-manifest-1
    RUN [ -f /var/lib/rpmmanifest/container-manifest-2 ] && grep -q "moby-runc[[:space:]]${RUNC_VERSION}" /var/lib/rpmmanifest/container-manifest-2
    RUN [ ! -d /var/lib/rpm ]
    EOT

    cache-from = ["type=gha,scope=dalec/test-runc/${distro}"]
    cache-to = ["type=gha,scope=dalec/test-runc/${distro},mode=max"]
}

target "test-fixture" {
    name = "test-fixture-${f}"
    matrix = {
        f = ["http-src", "nested", "frontend", "local-context", "cmd-src-ref"]
        tgt = ["mariner2/rpm"]
    }
    contexts = {
        "mariner2-toolchain" = "target:mariner2-toolchain"
    }
    dockerfile = "test/fixtures/${f}.yml"

    args = {
        "BUILDKIT_SYNTAX" = FRONTEND_REF
    }
    target = tgt
    cache-from = ["type=gha,scope=dalec/${tgt}/${f}"]
    cache-to = ["type=gha,scope=dalec/${tgt}/${f},mode=max"]
}

variable "BUILD_SPEC" {
    default = "\"ERROR: must set BUILD_SPEC variable to the path to the build spec file\""
}

target "build" {
    name = "build-${distro}-${tgt}"
    matrix = {
        distro = ["mariner2"]
        tgt = ["rpm", "container", "toolkitroot"]
    }
    contexts = {
        "mariner2-toolchain" = "target:mariner2-toolchain"
    }
    dockerfile = BUILD_SPEC
    args = {
        "BUILDKIT_SYNTAX" = FRONTEND_REF
    }
    target = "${distro}/${tgt}"
    // only tag the container target
    tags = tgt == "container" ? ["build:${distro}"] : []
    // only output non-container targets to the fs
    output = tgt != "container" ? ["_output"] : []

    cache-from = ["type=gha,scope=dalec/${distro}/${tgt}"]
    cache-to = ["type=gha,scope=dalec/${distro}/${tgt},mode=max"]
}