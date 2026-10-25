# Build stage
FROM golang:1.27.3-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o server ./cmd/server

# Runtime stage
FROM alpine:3.24.3
RUN apk --no-cache add ca-certificates tzdata
RUN chmod 755 /root
WORKDIR /root/
COPY --from=builder /app/server .
USER 65534:65534
