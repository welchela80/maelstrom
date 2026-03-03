# =============================================================================
# Build stage
# =============================================================================
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy module files and download dependencies
COPY synthetic-data/go.mod synthetic-data/go.sum ./
RUN go mod download

# Copy source files — consumer only
COPY synthetic-data/consumer.go .
COPY synthetic-data/maelstrom.go .
COPY synthetic-data/graphql.go .

# Build a statically linked binary for linux/amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o sensor-consumer .

# =============================================================================
# Runtime stage — minimal image, non-root user per IDD cybersecurity requirements
# =============================================================================
FROM gcr.io/distroless/static:nonroot

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/sensor-consumer .

# CSV is copied into the build context by docker-compose before building.
# See docker-compose.yml for the context path.
COPY files/sensor_operational_range.csv files/sensor_operational_range.csv

# Non-root user enforced by distroless:nonroot base image (uid 65532)
# Satisfies IDD §Container Hardening: "Running containers as non-root users"

ENTRYPOINT ["/app/sensor-consumer", "files/sensor_operational_range.csv"]