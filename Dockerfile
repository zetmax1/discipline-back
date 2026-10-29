# Base images are pinned by version and multi-arch index digest; bump both together.
FROM golang:1.26.10-alpine3.24@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS builder

WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/discipline-backend ./cmd/server/main.go

# Run stage
FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

# Unprivileged runtime user
RUN addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app

WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/discipline-backend .

USER 10001:10001

# Expose port (Railway overrides this with its own PORT variable)
EXPOSE 8080

CMD ["./discipline-backend"]
