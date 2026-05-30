# Stage 1: Build the admin web application
FROM oven/bun:1 AS web-builder

WORKDIR /app/cmd/server/web

COPY cmd/server/web/package.json ./
COPY cmd/server/web/bun.lock ./
RUN bun install --frozen-lockfile

COPY cmd/server/web ./
RUN bun run build

# Stage 2: Build the Go application
FROM golang:1.25-alpine AS builder

ARG VERSION=dev

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

RUN apk add --no-cache git protoc protobuf-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web-builder /app/cmd/server/web/dist ./cmd/server/web/dist

# Generate protobuf
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
    PATH="$PATH:/root/go/bin" protoc --go_out=. --go_opt=paths=source_relative \
      --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/tunnel.proto

RUN go build -o http-tunnels-server -ldflags "-s -w -X main.Version=${VERSION}" ./cmd/server && \
    go build -o http-tunnels -ldflags "-s -w -X main.Version=${VERSION}" ./cmd/client

# Stage 3: Create a lightweight runtime image
FROM alpine:latest

RUN apk add --no-cache ca-certificates && mkdir -p /data

WORKDIR /root/

COPY --from=builder /app/http-tunnels-server ./
COPY --from=builder /app/http-tunnels ./
COPY --from=builder /app/cmd/server/web/dist ./cmd/server/web/dist

VOLUME ["/data"]

EXPOSE 8443 8080

CMD ["./http-tunnels-server"]
