# Stage 1: Build the Go application
FROM golang:1.23-alpine AS builder

# Set environment variables
ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

# Install necessary dependencies
RUN apk add --no-cache git

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the Go application
RUN go build -o main -ldflags "-s -w" cmd/server/main.go

# Stage 2: Create a lightweight runtime image
FROM alpine:latest

# Install certificates for HTTPS
RUN apk add --no-cache ca-certificates

# Set the working directory
WORKDIR /root/

# Copy the compiled binary from the builder stage
COPY --from=builder /app/main .

# Expose the port the application listens on
EXPOSE 80

# Command to run the application
CMD ["./main"]