FROM --platform=${BUILDPLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.23@sha256:40eca8471660cee37f90c16dadc50dd216d14e1f13d8f7787480bb496047afca AS go

FROM go  AS frontend-build
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0
ARG TARGETARCH TARGETOS GOFLAGS=-trimpath
ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOFLAGS=${GOFLAGS}
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /frontend ./cmd/frontend && \
    go build -o /dalec-redirectio ./cmd/dalec-redirectio

FROM scratch AS frontend
COPY --from=frontend-build /frontend /frontend
COPY --from=frontend-build /dalec-redirectio /dalec-redirectio
LABEL moby.buildkit.frontend.network.none="true"
LABEL moby.buildkit.frontend.caps="moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests,moby.buildkit.frontend.contexts"
ENTRYPOINT ["/frontend"]
