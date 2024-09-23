FROM golang:1.23.1-alpine AS builder

WORKDIR /app

COPY . .

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o health-monitor /app/main.go

FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/health-monitor /app/health-monitor
COPY --from=builder /app/config /app/config

CMD ["/app/health-monitor"]
