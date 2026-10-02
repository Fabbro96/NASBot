# ============================================================================
# NASBot - container image (SECONDARY deployment path)
#
# The supported way to run NASBot on a NAS is the native binary produced by
# scripts/package_runtime.sh and driven by start_bot.sh. This image exists for
# users who prefer a container; it needs much more privilege than a normal
# service (see the annotations below).
#
# The build context is filtered by .dockerignore. That file is NOT optional:
# `COPY . .` would otherwise put config.json (bot_token, gemini_api_key), the
# whole .git history and the local build artifacts into the builder stage.
# ============================================================================

# golang 1.23 matches the deadlock/race gate (.github/workflows/
# deadlock-race-gate.yml runs 1.23.x) and is >= the 1.22 declared in go.mod,
# so the image builds the same code the gates test.
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Dependencies first: this layer is cached until go.mod/go.sum change, so a
# source-only edit does not re-download the module cache.
COPY go.mod go.sum ./
RUN go mod download

# Everything else. Filtered by .dockerignore: secrets and build artifacts never
# reach this layer.
COPY . .
ARG VERSION=dev

# CGO_ENABLED=0 produces a static binary that runs on a scratch/alpine base and
# keeps the final image small; the NAS runtime bundle is built the same way.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.Version=${VERSION}" -o nasbot .

# ----------------------------------------------------------------------------
# Runtime stage
# ----------------------------------------------------------------------------
# alpine:3.19 (EOL) -> still the pinned tag used by the published image.
# NOT bumped here on purpose: this repository cannot query the registry to
# confirm that a replacement tag exists, and a wrong tag turns a working
# secondary path into a failed build. Bump it in a separate, tested change.
FROM alpine:3.24

# Tooling the bot actually shells out to when monitoring the host:
#   smartmontools  -> SMART / disk health
#   docker-cli     -> container list, logs, start/stop, prune
#   lm-sensors     -> temperatures
#   dmidecode      -> hardware model, memory modules
#   util-linux     -> blkid, lsblk, mount information
#   iputils/curl   -> network reachability checks
#   tzdata         -> correct timestamps in reports
RUN apk add --no-cache \
    smartmontools \
    docker-cli \
    tzdata \
    ca-certificates \
    curl \
    iputils \
    lm-sensors \
    dmidecode \
    util-linux

ENV NASBOT_DOCKER=true
WORKDIR /app
COPY --from=builder /app/nasbot .

# The container runs as root on purpose: reading /dev, /sys and the SMART data
# of the disks, and talking to the Docker daemon, all require it. Dropping the
# USER here without changing docker-compose.yml would break disk and container
# monitoring at runtime rather than at build time.

# Declares the Docker socket as a volume so that a bind mount of the host
# socket onto this path is accepted by Docker. It grants full control of the
# host's container runtime: any process able to run here can start privileged
# containers and thus mount the host filesystem. It is required by the
# /docker commands, which is why the primary (native binary) deployment is
# preferred on a NAS.
VOLUME ["/var/run/docker.sock"]

# Note: the working directory is bind-mounted with config.json in
# docker-compose.yml. The image itself ships no credentials.
CMD ["./nasbot"]
