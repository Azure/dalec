FROM golang:1.20 AS build
WORKDIR /build
COPY . .
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /frontend ./cmd/frontend-mariner2

FROM golang:1.21 AS build-test
WORKDIR /build
COPY . .
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /frontend ./cmd/frontend-rpm-bundle

FROM almalinux:8 AS rpmbuild
RUN yum install -y rpmdevtools gcc git make yum-utils
RUN yum-config-manager --enable powertools || yum-config-manager --enable resilientstorage
RUN dnf install -y gcc libseccomp-devel libtool libtool-ltdl-devel make which
COPY _output2/ /root/rpmbuild/
RUN rpmbuild -bb /root/rpmbuild/SPECS/moby-runc.spec
RUN ls -lh /root/rpmbuild/RPMS/*; exit 1

FROM scratch
COPY --from=build-test /frontend /frontend
LABEL moby.buildkit.frontend.caps=moby.buildkit.frontend.subrequests
ENTRYPOINT ["/frontend"]
