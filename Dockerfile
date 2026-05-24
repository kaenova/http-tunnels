# Stage 1: Build the admin web application
FROM oven/bun:1 AS web-builder

WORKDIR /app/cmd/server/web

COPY cmd/server/web/package.json ./
COPY cmd/server/web/bun.lock ./
RUN bun install --frozen-lockfile

COPY cmd/server/web ./
RUN bun run build

# Stage 2: Build the Go application
FROM golang:1.23-alpine AS builder

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

RUN apk add --no-cache git

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-builder /app/cmd/server/web/dist ./cmd/server/web/dist

RUN go build -o main -ldflags "-s -w" ./cmd/server

# Stage 3: Create a lightweight runtime image
FROM alpine:latest

RUN apk add --no-cache ca-certificates && mkdir -p /data

ENV LISTEN_ADDR=:80 \
    DB_PATH=/data/http-tunnels.db \
    COOKIE_SECURE=false

WORKDIR /root/

COPY --from=builder /app/main ./main

VOLUME ["/data"]

EXPOSE 80

CMD ["./main"]
