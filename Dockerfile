FROM golang:1.20-alpine as builder

WORKDIR /app

# Install dependencies
RUN apk add --no-cache git

# Copy only the go.mod and go.sum first to leverage Docker layer caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY *.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o eduroam-idp .

# Use a minimal alpine image for the final image
FROM alpine:3.17

WORKDIR /app

# Install dependencies
RUN apk add --no-cache ca-certificates tzdata

# Copy the binary from the builder stage
COPY --from=builder /app/eduroam-idp .

# Copy configuration template
COPY qw-auth.properties.template ./

# Create output directory
RUN mkdir -p /app/output

# Create volume for persistent storage
VOLUME /app/output

# Environment variables with defaults
ENV NUM_WORKERS=10
ENV TZ=Asia/Bangkok

# Entrypoint
ENTRYPOINT ["/app/eduroam-idp"]

# Default command if none is provided
CMD ["--help"]