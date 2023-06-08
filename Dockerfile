FROM golang:1.20 AS build
WORKDIR /build
COPY . .
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /fontend ./cmd/frontend-mariner2

FROM scratch
COPY --from=build /fontend /fontend
ENTRYPOINT ["/fontend"]
