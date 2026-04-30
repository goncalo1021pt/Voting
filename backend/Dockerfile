# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY backend/go.mod backend/go.sum* ./
RUN go mod download 2>/dev/null || true

COPY backend/srcs/ ./srcs/
COPY frontend/ ./frontend/

RUN go build -o voting-server ./srcs

# Runtime stage
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/voting-server .
COPY --from=builder /app/frontend ./frontend

EXPOSE 8080

CMD ["./voting-server"]
