FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-X main.Version=${VERSION}" -o nasbot .

FROM alpine:3.19

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

# Required so that the user can mount their docker sock
VOLUME ["/var/run/docker.sock"]

CMD ["./nasbot"]
