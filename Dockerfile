# Build stage
FROM golang:1.23.1-alpine AS builder

# Set the working directory
WORKDIR /app

# Copy go.mod and go.sum
COPY . .

# Download dependencies
RUN go mod download

# Build the application (assuming main.go is in the root directory or as part of a package)
RUN CGO_ENABLED=0 GOOS=linux go build -o health-monitor /app/main.go

# Final stage
FROM alpine:latest

# Set working directory
WORKDIR /app

# Copy the binary from builder
COPY --from=builder /app/health-monitor /app/health-monitor
COPY --from=builder /app/config /app/config

CMD ["/app/health-monitor"]
