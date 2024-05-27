FROM --platform=${BUILDPLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.21@sha256:ac43b1ed13ad1315cffa2690fc037aebc947837d296f5245a2770e68414d282c AS go

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
