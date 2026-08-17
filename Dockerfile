# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod and sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the Go app
RUN CGO_ENABLED=0 GOOS=linux go build -o main ./cmd/server/main.go

# Run stage
FROM alpine:3.19

WORKDIR /app

# Copy the compiled binary from the builder
COPY --from=builder /app/main .

# Expose port (default 8080 or PORT env var)
ENV PORT 8080
EXPOSE 8080

# Run the binary
CMD ["./main"]
