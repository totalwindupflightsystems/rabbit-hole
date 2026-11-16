# =============================================================================
# Rabbit-Hole — Agent Legibility Daemon
# Multi-stage Docker build
# =============================================================================
# Build stage
# -----------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

# The builder toolchain must match go.mod's `go` directive (1.26.0) and must
# never be auto-downloaded inside the image: an air-gapped/offline builder
# would otherwise fail the build, and a silently fetched toolchain makes the
# image non-reproducible.
ENV GOTOOLCHAIN=local

RUN apk add --no-cache ca-certificates tzdata

# Build metadata (DF-021). .git is excluded from the build context, so
# these cannot be derived here — pass them at build time, e.g.:
#   docker build --build-arg VERSION=$(git describe --tags) \
#     --build-arg COMMIT=$(git rev-parse --short HEAD) \
#     --build-arg BUILD_DATE=$(git log -1 --format=%cd --date=iso-strict) .
ARG VERSION=unknown
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /app

# Cache dependency downloads in a separate layer
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_DATE}" \
    -o /app/bin/rabbit-hole \
    ./cmd/rabbit-hole/

# -----------------------------------------------------------------------------
# Runtime stage
# -----------------------------------------------------------------------------
FROM alpine:latest

# OCI labels
LABEL org.opencontainers.image.source="https://gitlab.readydedis.com/rabbit-hole/rabbit-hole"
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
