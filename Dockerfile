# syntax=docker/dockerfile:1
# check=error=true

# --- Base Images -------------------------------------------------------------

ARG BUILD_IMAGE=golang:1.27.3
ARG BUILD_SHA256=sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b

ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot
ARG RUNTIME_SHA256=sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

# --- Builder -----------------------------------------------------------------

FROM --platform=$BUILDPLATFORM ${BUILD_IMAGE}@${BUILD_SHA256} AS build
WORKDIR /src

COPY go.mod go.sum* ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/

ARG TARGETOS TARGETARCH

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' -o /out/mingest ./cmd/mingest \
 && mkdir /out/data

# --- Production  -------------------------------------------------------------

FROM ${RUNTIME_IMAGE}@${RUNTIME_SHA256}

LABEL org.opencontainers.image.title="mingest" \
      org.opencontainers.image.description="mingest - Mail Ingestion Service"

COPY --from=build /out/mingest /usr/local/bin/mingest
COPY --from=build --chown=65532:65532 /out/data /var/lib/mingest

ENV MINGEST_DATA_DIR=/var/lib/mingest

USER 65532:65532
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/mingest", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/mingest"]
