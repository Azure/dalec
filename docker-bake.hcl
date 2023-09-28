group "default" {
    targets = ["frontend", "toolchain"]
}

target "frontend" {
    target = "frontend"
    tags = ["ghcr.io/azure/dalec/frontend:latest", "local/dalec/frontend"]
}

// Toolchain builds the full mariner container with the mariner build tookit
target "toolchain" {
    dockerfile = "./frontend/mariner2/Dockerfile"
    target = "toolchain"
    tags = ["ghcr.io/azure/dalec/mariner/toolchain:latest", "local/dalec/mariner/toolchain"]
}

variable "RUNC_COMMIT" {
    default = "v1.1.9"
}

variable "RUNC_VERSION" {
    default = "1.1.9"
}

variable "BUILDKIT_SYNTAX" {
    default = "local/dalec/frontend"
}

target "runc" {
    name = "runc-${distro}-${tgt}"
    dockerfile = "./frontend/${distro}/test/fixtures/moby-runc.yml"
    args = {
        "RUNC_COMMIT" = RUNC_COMMIT
        "VERSION" = RUNC_VERSION
        "BUILDKIT_SYNTAX" = BUILDKIT_SYNTAX
    }
    matrix = {
        distro = ["mariner2"]
        tgt = ["rpm", "container", "toolkitroot"]
    }
    target = "${distro}/${tgt}"
    // only tag the container target
    tags = tgt == "container" ? ["runc:${distro}"] : []
    // only output non-container targets to the fs
    output = tgt != "container" ? ["_output"] : []
}
