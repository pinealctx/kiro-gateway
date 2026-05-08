# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o kiro-gateway .

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/kiro-gateway .
COPY --from=builder /build/config.example.yaml ./config.example.yaml

EXPOSE 8080

ENTRYPOINT ["./kiro-gateway"]
