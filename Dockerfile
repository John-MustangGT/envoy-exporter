# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY . .
RUN go mod tidy
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o envoy-prometheus-exporter .

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app

COPY --from=builder /app/envoy-prometheus-exporter .

# Create a non-root user
RUN addgroup -g 1001 envoy && \
    adduser -D -s /bin/sh -u 1001 -G envoy envoy

USER envoy

EXPOSE 8080

CMD ["./envoy-prometheus-exporter", "envoy_config.xml"]