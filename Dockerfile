# Runtime image for BaaS.
#
# It builds on top of the base image produced by base.Dockerfile, which carries
# Chrome, Pandoc, Node.js and the document-processing tooling. Build the base
# first, then this file:
#
#   docker build -f base.Dockerfile -t baas-base:local .
#   docker build -t baas:local .
#
# Point --build-arg BASE_IMAGE=... at your own registry copy to skip the first
# step in CI.

# Build stage: compile the Go binary.
FROM golang:1.25.5-alpine AS build-stage
ARG VERSION=0.0.0

RUN apk add --no-cache binutils ca-certificates

WORKDIR /src

# Cache dependencies separately from the source tree.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && go mod verify

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w -X github.com/Ursa-Minor-Beta/baas/internal/build.Version=${VERSION}" \
        -o /out/baas ./cmd/baas && \
    strip /out/baas

# Runtime stage.
ARG BASE_IMAGE=baas-base:local
FROM ${BASE_IMAGE}

ARG VERSION=0.0.0
ARG AIPANDOC_COMMIT="3ac23e0829d7426a4026216dff2f78721095e04c"

LABEL org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.title="BaaS" \
      org.opencontainers.image.description="Browser as a Service — browser automation and document extraction over HTTP" \
      org.opencontainers.image.source="https://github.com/Ursa-Minor-Beta/baas" \
      org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /app

COPY --from=build-stage --chown=appuser:appgroup /out/baas /app/baas

COPY --chmod=550 --chown=appuser:appgroup entrypoint.sh /app/entrypoint.sh
COPY --chown=appuser:appgroup profile /app/profile/
COPY --chown=appuser:appgroup scripts /app/scripts/
COPY --chown=appuser:appgroup package.json package-lock.json /app/

# Node helpers back the Markdown, PDF and HTML rendering endpoints.
USER root
RUN npm ci --omit=dev && \
    npm cache clean --force && \
    chown -R appuser:appgroup /app/node_modules && \
    chmod 550 /app/baas /app/entrypoint.sh && \
    chmod 644 /app/scripts/*.mjs /app/scripts/*.py 2>/dev/null || true

USER appuser

# Base environment (HOME, XDG dirs, SCRIPTS_DIR, ...) is inherited from the base image.
ENV AIPANDOC_COMMIT="${AIPANDOC_COMMIT}" \
    ALLOWED_IPS="0.0.0.0" \
    ALLOWED_ORIGINS="http://localhost:8080,https://localhost:8080"

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
    CMD curl -f --max-time 5 http://localhost:8080/health || exit 1

ENTRYPOINT ["dumb-init", "--", "/app/entrypoint.sh"]
