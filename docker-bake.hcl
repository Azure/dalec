group "default" {
    targets = ["frontend"]
}

group "test" {
    targets = ["test-runc", "test-fixture"]
}

target "frontend" {
    target = "frontend"
    tags = ["ghcr.io/azure/dalec/frontend:latest", BUILDKIT_SYNTAX]
}

target "mariner2-toolchain" {
    dockerfile = "./frontend/mariner2/Dockerfile"
    target = "toolchain"
    tags = ["ghcr.io/azure/dalec/mariner2/toolchain:latest", "local/dalec/mariner2/toolchain"]
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

variable "BUILDKIT_SYNTAX" {
    default = "local/dalec/frontend"
}

target "runc" {
    name = "runc-${distro}-${tgt}"
    dockerfile = "test/fixtures/moby-runc.yml"
    args = {
        "RUNC_COMMIT" = RUNC_COMMIT
        "VERSION" = RUNC_VERSION
        "BUILDKIT_SYNTAX" = BUILDKIT_SYNTAX
        "REVISION" = RUNC_REVISION
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
    EOT
}

target "test-fixture" {
    name = "test-fixture-${f}"
    matrix = {
        f = ["http-src", "nested", "frontend"]
        tgt = ["mariner2/rpm"]
    }
    contexts = {
        "mariner2-toolchain" = "target:mariner2-toolchain"
    }
    dockerfile = "test/fixtures/${f}.yml"
    args = {
        "BUILDKIT_SYNTAX" = BUILDKIT_SYNTAX
    }
    target = tgt
    cache-from = ["type=gha,scope=dalec/${tgt}/${f}"]
    cache-to = ["type=gha,scope=dalec/${tgt}/${f},mode=max"]
}
