# syntax=docker/dockerfile:1.6-labs

FROM mcr.microsoft.com/oss/go/microsoft/golang:1.21 AS go

FROM go AS frontend-build
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /frontend ./cmd/frontend


FROM mcr.microsoft.com/cbl-mariner/base/core:2.0 AS toolchain-build
RUN \
    --mount=type=cache,target=/var/tdnf/cache,id=mariner2-tdnf-cache \
    tdnf install -y \
        binutils \
        bison \
        ca-certificates \
        curl \
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
ARG TOOLKIT_COMMIT=f3fee7cccffb21f1d7abf5ff940ba7db599fd4a2
ADD --keep-git-dir https://github.com/microsoft/CBL-Mariner.git#${TOOLKIT_COMMIT} /build
WORKDIR /build
ENV CACHED_RPMS_DIR=/root/.cache/mariner2-toolkit-rpm-cache
RUN \
    --security=insecure \
    --mount=type=cache,target=/go/pkg/mod,id=go-pkg-mod \
    --mount=type=cache,target=/root/.cache/go-build,id=go-build-cache \
    cd toolkit && make package-toolkit REBUILD_TOOLS=y 
RUN mkdir -p /tmp/toolkit && tar -C /tmp/toolkit --strip-components=1 -zxf /build/out/toolkit-*.tar.gz

FROM scratch AS toolkit
COPY --from=toolchain-build /tmp/toolkit /

FROM scratch AS frontend
COPY --from=frontend-build /frontend /frontend
LABEL moby.buildkit.frontend.network.none="true"
LABEL moby.buildkit.frontend.caps="moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests"
ENTRYPOINT ["/frontend"]
