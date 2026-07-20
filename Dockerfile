FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o nasbot .

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

WORKDIR /app
COPY --from=builder /app/nasbot .

# Required so that the user can mount their docker sock
VOLUME ["/var/run/docker.sock"]

CMD ["./nasbot"]
