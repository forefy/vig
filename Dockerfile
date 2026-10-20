# syntax=docker/dockerfile:1
#
# vig: one image, three stages. The runtime carries a static Go binary, the
# operator CLI and the prerendered frontend; no toolchain, no shell.
#
#   web     : Vite build + prerender  -> /web/dist        (build platform)
#   build   : Go, CGO off, cross-compiled to $TARGETARCH  (build platform)
#   runtime : distroless static, nonroot
#
# Multi-arch is driven by buildx (--platform linux/amd64,linux/arm64): the
# first two stages run natively on the builder and Go cross-compiles.

# --- Stage 1: frontend -------------------------------------------------------
FROM --platform=$BUILDPLATFORM node:20-alpine AS web
WORKDIR /web
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
# The generated client is committed; the spec is only needed for `npm run gen`.
COPY backend/openapi.json ../backend/openapi.json
# Canonical origin baked into canonical/OG tags and the sitemap. Empty is fine
# for local images; prod passes its host.
ARG VITE_SITE_URL=
ARG SITE_URL=
ENV VITE_SITE_URL=${VITE_SITE_URL} SITE_URL=${SITE_URL:-$VITE_SITE_URL}
RUN npm run build && rm -rf dist/server

# --- Stage 2: Go -------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.26 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    export CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}; \
    LD="-s -w -X github.com/forefy/vig/backend/internal/api.Version=${VERSION} -X github.com/forefy/vig/backend/internal/api.Commit=${COMMIT}"; \
    go build -trimpath -ldflags "${LD}" -o /out/vig ./cmd/vig; \
    go build -trimpath -ldflags "${LD}" -o /out/vig-op ./cmd/vig-op; \
    mkdir -p /out/data

# --- Stage 3: runtime --------------------------------------------------------
# static-debian12 ships ca-certificates and tzdata (TLS to Postgres, GitHub,
# RPC) and nothing else. nonroot is uid 65532; /data is the only writable path
# and the chart mounts a volume over it.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vig /out/vig-op /usr/local/bin/
COPY --from=build --chown=nonroot:nonroot /out/data /data
COPY --from=web /web/dist /srv/web
ENV STATIC_DIR=/srv/web DATA_DIR=/data PORT=7112 VIG_ENV=prod LOG_FORMAT=json
EXPOSE 7112
ENTRYPOINT ["vig"]
