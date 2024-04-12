# syntax=docker/dockerfile:1.6

FROM scratch as creds

FROM --platform=${BUILDPLATFORM} mcr.microsoft.com/oss/go/microsoft/golang:1.21@sha256:f5fc8bf2873d2fa538b2d87e6e8f9bc6d05a3c419a840147b25f6386855192e9 AS signer-builder
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0
ARG TARGETARCH TARGETOS GOFLAGS=-trimpath

ENV GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOFLAGS=${GOFLAGS}
RUN \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /signer ./cmd/signer

FROM mcr.microsoft.com/azure-cli:cbl-mariner2.0 as az-base
WORKDIR /root
COPY ./tmp/xsignextension-0.46-py3-none-any.whl .
ARG XSIGNEXTENSION_VERSION=0.46
RUN <<EOF
az config set extension.use_dynamic_install=yes_without_prompt
az extension add \
  -s xsignextension-${XSIGNEXTENSION_VERSION}-py3-none-any.whl \
  -y --system --debug
EOF


FROM az-base as final
COPY --from=signer-builder /signer /signer
COPY --from=creds .azure /root/.azure
LABEL moby.buildkit.frontend.network.none="true"
LABEL moby.buildkit.frontend.caps="moby.buildkit.frontend.inputs,moby.buildkit.frontend.subrequests,moby.buildkit.frontend.contexts"
ENTRYPOINT ["/signer"]
