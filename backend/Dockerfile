# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/srcs/ ./srcs/
COPY frontend/ ./frontend/

RUN go build -o events-server ./srcs

# Runtime stage — pinned so rebuilds are reproducible; bump deliberately.
FROM alpine:3.24

RUN adduser -D -H events

WORKDIR /app

COPY --from=builder /app/events-server .
COPY --from=builder /app/frontend ./frontend

USER events

EXPOSE 8080

CMD ["./events-server"]
