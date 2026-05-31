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

# Generate protobuf (v5 uses proto/frame.proto)
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    PATH="$PATH:/root/go/bin" protoc --go_out=. --go_opt=paths=source_relative proto/frame.proto

# Build server binary only (client is at root http_tunnels.go)
RUN go build -o http-tunnels-server -ldflags "-s -w -X main.Version=${VERSION}" ./cmd/server && \
    go build -o http-tunnels -ldflags "-s -w -X main.Version=${VERSION}" .

# Stage 3: Create a lightweight runtime image
FROM alpine:latest

RUN apk add --no-cache ca-certificates && mkdir -p /data

WORKDIR /root/

# Copy binary and rename to 'main' for backward compatibility with existing deployments
COPY --from=builder /app/http-tunnels-server ./main
COPY --from=builder /app/http-tunnels ./
COPY --from=builder /app/cmd/server/web/dist ./cmd/server/web/dist

VOLUME ["/data"]

EXPOSE 80

ENV LISTEN_ADDR=:80
ENV DB_PATH=/data/http-tunnels.db
ENV COOKIE_SECURE=false

ENTRYPOINT ["./main"]
CMD []