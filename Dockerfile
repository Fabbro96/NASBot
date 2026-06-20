FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o nasbot cmd/bot/main.go
RUN CGO_ENABLED=0 GOOS=linux go build -o fswatchdog cmd/fswatchdog/main.go

FROM alpine:3.19

RUN apk add --no-cache \
    smartmontools \
    docker-cli \
    tzdata \
    ca-certificates \
    curl \
    iputils \
    lm-sensors \
    dmidecode

WORKDIR /app
COPY --from=builder /app/nasbot .
COPY --from=builder /app/fswatchdog .

# Required so that the user can mount their docker sock
VOLUME ["/var/run/docker.sock"]

CMD ["./nasbot"]
