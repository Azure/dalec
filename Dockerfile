FROM mcr.microsoft.com/oss/go/microsoft/golang:1.21 AS go

FROM go AS frontend-build
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /frontend ./cmd/frontend


FROM scratch AS frontend
COPY --from=frontend-build /frontend /frontend
LABEL moby.buildkit.frontend.network.none="true"
LABEL moby.buildkit.frontend.caps="moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests"
ENTRYPOINT ["/frontend"]
