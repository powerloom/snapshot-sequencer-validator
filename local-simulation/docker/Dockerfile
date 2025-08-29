FROM golang:1.24.5-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git build-base

WORKDIR /app

# Copy all service source code
COPY ../../submissions-bootstrap-node ./submissions-bootstrap-node/
COPY ../../libp2p-submission-sequencer-listener ./libp2p-submission-sequencer-listener/
COPY ../../p2p-debugger ./p2p-debugger/

# Build argument to determine which service to build
ARG SERVICE_TYPE=bootstrap

# Build the appropriate service based on SERVICE_TYPE
RUN if [ "$SERVICE_TYPE" = "bootstrap" ]; then \
        cd submissions-bootstrap-node && \
        go mod tidy && \
        CGO_ENABLED=0 GOOS=linux go build -o /bootstrap-service ./cmd/main.go; \
    elif [ "$SERVICE_TYPE" = "sequencer" ]; then \
        cd libp2p-submission-sequencer-listener && \
        go mod tidy && \
        CGO_ENABLED=0 GOOS=linux go build -o /sequencer-service ./cmd/main.go; \
    elif [ "$SERVICE_TYPE" = "debugger" ]; then \
        cd p2p-debugger && \
        go mod tidy && \
        CGO_ENABLED=0 GOOS=linux go build -o /debugger-service .; \
    fi

# Final runtime image
FROM alpine:latest

# Install runtime dependencies and network tools
RUN apk add --no-cache \
    ca-certificates \
    netcat-openbsd \
    iproute2 \
    iptables \
    tcpdump \
    curl \
    bash \
    && rm -rf /var/cache/apk/*

WORKDIR /app

# Build argument to determine which service binary to copy
ARG SERVICE_TYPE=bootstrap

# Copy the appropriate binary and entrypoint scripts
RUN if [ "$SERVICE_TYPE" = "bootstrap" ]; then \
        echo "Copying bootstrap service"; \
    elif [ "$SERVICE_TYPE" = "sequencer" ]; then \
        echo "Copying sequencer service"; \
    elif [ "$SERVICE_TYPE" = "debugger" ]; then \
        echo "Copying debugger service"; \
    fi

# Copy binaries conditionally
COPY --from=builder /bootstrap-service* ./
COPY --from=builder /sequencer-service* ./
COPY --from=builder /debugger-service* ./

# Copy entrypoint script
COPY entrypoint.sh ./
RUN chmod +x entrypoint.sh

# Create directories for results and configs
RUN mkdir -p /app/results /app/configs /app/logs

# Environment variables with defaults
ENV SERVICE_TYPE=bootstrap
ENV P2P_PORT=9100
ENV API_PORT=8081
ENV LOG_LEVEL=INFO
ENV NODE_ID=default-node

# Health check script
COPY healthcheck.sh ./
RUN chmod +x healthcheck.sh

EXPOSE $P2P_PORT $API_PORT

ENTRYPOINT ["./entrypoint.sh"]