# Build stage
FROM golang:1.24.5-alpine AS builder

RUN apk add --no-cache git make gcc musl-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary into bin directory
RUN mkdir -p bin && go build -o bin/sequencer-consensus-test ./cmd/sequencer-consensus-test/main.go

# Runtime stage
FROM alpine:latest

RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bin/sequencer-consensus-test /app/sequencer-consensus-test

# Create data directory
RUN mkdir -p /data

# Run the sequencer consensus test
ENTRYPOINT ["/app/sequencer-consensus-test"]