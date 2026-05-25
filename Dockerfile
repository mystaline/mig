# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /mig ./cmd/main.go

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates curl

COPY --from=builder /mig /usr/local/bin/mig

# Default environment for migrations directory
ENV MIGRATIONS_DIR=/migrations

WORKDIR /

ENTRYPOINT ["mig"]
