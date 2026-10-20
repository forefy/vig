# syntax=docker/dockerfile:1
#
# The audit sandbox. One container per job: it claims, checks out the customer's
# source at the pinned commit, builds it with the Solana toolchain so the
# bytecode can be compared with what is deployed, and posts results through
# vig-op. The container is destroyed after delivery, so nothing customer-owned
# outlives the job. vig-op refuses to run anywhere else.
#
#   docker build -f deploy/operator/Dockerfile -t vig-operator .
#   docker run --rm -it -e VIG_API=https://api.example -e VIG_OP_TOKEN=vop_... vig-operator
#   # inside: vig-op claim && solana-verify build && vig-op verify && ...

# --- Stage 1: vig-op --------------------------------------------------------------
FROM --platform=$BUILDPLATFORM golang:1.23 AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY backend/ ./
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/vig-op ./cmd/vig-op

# --- Stage 2: Solana toolchain --------------------------------------------------------
# rust:1 carries cargo; solana-verify and the Anza CLI give reproducible builds
# and the same executable hash the chain client computes.
FROM rust:1-bookworm AS toolchain
ARG SOLANA_VERSION=2.1.6
ARG SOLANA_VERIFY_VERSION=0.4.6
RUN apt-get update && apt-get install -y --no-install-recommends git ca-certificates pkg-config libssl-dev libudev-dev && rm -rf /var/lib/apt/lists/*
RUN cargo install solana-verify --version ${SOLANA_VERIFY_VERSION} --locked
RUN sh -c "$(curl -sSfL https://release.anza.xyz/v${SOLANA_VERSION}/install)" && \
    ln -s /root/.local/share/solana/install/active_release/bin/* /usr/local/bin/

# --- Stage 3: sandbox -----------------------------------------------------------------
FROM toolchain
COPY --from=build /out/vig-op /usr/local/bin/vig-op
# Every job gets an empty workspace. Nothing here is persisted between jobs.
RUN useradd --create-home --uid 10001 operator && mkdir -p /work && chown operator:operator /work
USER operator
WORKDIR /work
ENV VIG_OP_HOME=/work VIG_OP_CONTAINER=1 VIG_API=http://host.docker.internal:7112
ENTRYPOINT ["/bin/bash"]
