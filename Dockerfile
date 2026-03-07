FROM golang:1.24-bookworm AS builder

WORKDIR /app

# Copy dependency files and download
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the application
COPY . .

# Build the application
# We use standard go build; CGO is enabled by default on Debian which is fine
RUN go mod tidy
RUN go build -ldflags="-w -s" -o gocognigo ./cmd/server

# Runtime stage
FROM debian:bookworm-slim

WORKDIR /app

# Install runtime dependencies: 
# - tesseract-ocr: for local OCR
# - tesseract-ocr-eng: English language pack
# - poppler-utils: for pdftoppm (PDF to image conversion)
# - ca-certificates: for making HTTPS requests to cloud LLMs
RUN apt-get update && apt-get install -y --no-install-recommends \
    tesseract-ocr \
    tesseract-ocr-eng \
    tesseract-ocr-fra \
    tesseract-ocr-spa \
    tesseract-ocr-deu \
    poppler-utils \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Copy built binary from builder
COPY --from=builder /app/gocognigo .

# Copy web assets required for frontend
COPY --from=builder /app/web ./web

# Create data directory with permissive permissions for persistent storage
RUN mkdir -p /app/data && chmod 777 /app/data

# Default environment variables
ENV PORT=8080
ENV OCR_PROVIDER=tesseract

# Expose server port
EXPOSE 8080

# Entrypoint
CMD ["./gocognigo"]
