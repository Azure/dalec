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

FROM scratch
COPY --from=build-test /frontend /frontend
LABEL moby.buildkit.frontend.caps=moby.buildkit.frontend.subrequests
ENTRYPOINT ["/frontend"]
