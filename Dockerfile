FROM golang:alpine AS builder

WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /app/discipline-backend ./cmd/server/main.go

# Run stage
FROM alpine:latest
WORKDIR /app

# Copy the binary from the builder stage
COPY --from=builder /app/discipline-backend .

# Expose port (Railway overrides this with its own PORT variable)
EXPOSE 8080

CMD ["./discipline-backend"]
