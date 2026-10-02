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

# golang 1.27 is in the CI matrix (.github/workflows/ci.yml runs 1.22.x, 1.23.x
# and 1.27.x) and in the deadlock/race gate (1.23.x and 1.27.x), and is >= the
# 1.22 declared in go.mod, so the image is compiled by a toolchain the gates
# actually test rather than by one nothing exercises.
#
# This line is deliberately not a floating tag: a builder that changes under
# you turns a working secondary path into a failed build.
FROM golang:1.27-alpine AS builder

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
# -trimpath and -s -w match scripts/build_release.sh, which got them after the
# released binaries were found to carry 61 absolute paths of the build machine.
# Without them the image binary carries the builder's source paths and its whole
# symbol table, and nothing would notice the drift: CI never builds this file.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-X main.Version=${VERSION} -s -w" -o nasbot .

# ----------------------------------------------------------------------------
# Runtime stage
# ----------------------------------------------------------------------------
# The runtime base is the whole attack surface of the published image, so it
# does not sit on an EOL release: 3.19 is EOL, this moves to 3.24.
# The tag was confirmed to exist in the registry before the bump (Docker Hub
# library/alpine:3.24), which is the check that lets the previous "do not bump
# here on purpose" note be retired. Keep doing it by hand: a wrong tag turns
# this working secondary path into a failed build, and CI never builds the
# Dockerfile, so nothing would catch it.
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
