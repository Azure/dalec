FROM --platform=${BUILDPLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.21@sha256:f5e962fc0e36314548301b1b9380368f34dbff8f23c50d4a0f22a0be64da2552 AS go

FROM go  AS frontend-build
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
ARG TARGETARCH TARGETOS
ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH}
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -gcflags=all="-N -l" -o /frontend ./cmd/frontend && \
    go build -gcflags=all="-N -l" -o /dalec-redirectio ./cmd/dalec-redirectio

FROM scratch AS frontend
COPY --from=frontend-build /frontend /frontend
COPY --from=frontend-build /dalec-redirectio /dalec-redirectio
LABEL moby.buildkit.frontend.network.none="true"
LABEL moby.buildkit.frontend.caps="moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests,moby.buildkit.frontend.contexts"
ENTRYPOINT ["/frontend"]
