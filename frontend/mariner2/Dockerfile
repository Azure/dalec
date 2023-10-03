# syntax=mcr.microsoft.com/oss/moby/dockerfile:1.6.0
FROM mcr.microsoft.com/cbl-mariner/base/core:2.0 AS toolchain
RUN \
    --mount=type=cache,target=/var/tdnf/cache,id=mariner2-tdnf-cache \
    tdnf install -y \
        acl \
        binutils \
        bison \
        ca-certificates \
        curl \
        dnf \
        dnf-utils \
        gawk \
        git \
        glibc-devel \
        kernel-headers \
        make \
        msft-golang \
        python \
        rpm \
        rpm-build \
        wget
ARG TOOLKIT_COMMIT=8b1db59e9b011798e8e7750907f58b1bc9577da7
ADD --keep-git-dir https://github.com/microsoft/CBL-Mariner.git#${TOOLKIT_COMMIT} /build
ENV CONFIG_FILE="" CHROOT_DIR="/tmp/chroot"
# The toolchain can also run things in parallel so it will use multiple chroots.
# See
# https://github.com/microsoft/CBL-Mariner/blob/8b1db59e9b011798e8e7750907f58b1bc9577da7/toolkit/tools/internal/buildpipeline/buildpipeline.go#L37-L117
# for implementation of this.
RUN set -e; for ((n=0;n<$(($(nproc)*2));n++)); do mkdir -p "${CHROOT_DIR}/dalec${n}"; done;
RUN touch "${CHROOT_DIR}/chroot-pool.lock"
WORKDIR /build/toolkit
RUN \
    --mount=type=cache,target=/go/pkg/mod,id=go-pkg-mod \
    --mount=type=cache,target=/root/.cache/go-build,id=go-build-cache \
    # /.dockerenv is used by the toolkit to detect if we are running in a
    # container which will prevent this build from requring CAP_SYS_ADMIN
    trap 'rm /.dockerenv' EXIT; touch /.dockerenv; make -j$(nproc) go-tools REBUILD_TOOLS=y
RUN make toolchain -j$(nproc) chroot-tools REBUILD_TOOLS=n

