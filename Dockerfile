# =============================================================================
# Rabbit-Hole — Agent Legibility Daemon
# Multi-stage Docker build
# =============================================================================
# Build stage
# -----------------------------------------------------------------------------
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /app/bin/rabbit-hole \
    ./cmd/rabbit-hole/

# -----------------------------------------------------------------------------
# Runtime stage
# -----------------------------------------------------------------------------
FROM alpine:latest

# OCI labels
LABEL org.opencontainers.image.source="https://github.com/totalwindupflightsystems/rabbit-hole"
LABEL org.opencontainers.image.description="Rabbit-Hole — Agent Legibility Daemon. Attach eBPF probes to agent processes, classify syscall traces, and inspect via chat."
LABEL org.opencontainers.image.licenses="MIT"

# Runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create rabbit-hole user/group (matching systemd unit UID:GID 1000:1000)
RUN addgroup -S rabbit-hole && \
    adduser -S rabbit-hole -G rabbit-hole -h /var/lib/rabbit-hole -s /sbin/nologin

# Copy binary from build stage
COPY --from=builder /app/bin/rabbit-hole /usr/local/bin/rabbit-hole

# Create data directory with correct ownership
RUN mkdir -p /var/lib/rabbit-hole && \
    chown -R rabbit-hole:rabbit-hole /var/lib/rabbit-hole

# Port for chat/API layer
EXPOSE 8080

# Volume for persistent data
VOLUME /var/lib/rabbit-hole

# Switch to non-root user
USER rabbit-hole
WORKDIR /var/lib/rabbit-hole

ENTRYPOINT ["/usr/local/bin/rabbit-hole"]
CMD ["serve"]
