# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files if they exist
COPY go.mod go.sum* ./

# Download dependencies (if go.mod exists)
RUN go mod download 2>/dev/null || true

# Copy source code
COPY srcs/ ./srcs/
COPY frontend/ ./frontend/

# Build the application
RUN go build -o voting-server ./srcs

# Runtime stage
FROM alpine:latest

WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/voting-server .
COPY --from=builder /app/frontend ./frontend

# Expose port
EXPOSE 8080

# Run the application
CMD ["./voting-server"]
