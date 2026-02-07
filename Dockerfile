# Stage 1: Build frontend
FROM node:22-alpine AS ui-builder
WORKDIR /app/ui
COPY ui/package*.json ./
RUN npm ci
COPY ui/ ./
RUN npm run build

# Stage 2: Build Go binary
FROM golang:1.24-bookworm AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /app/ui/build ./ui/build
RUN go build -o cudascope ./cmd/cudascope/

# Stage 3: Runtime (minimal CUDA base with NVML)
FROM nvidia/cuda:12.8.0-base-ubuntu24.04

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=go-builder /app/cudascope /usr/local/bin/cudascope

EXPOSE 9090
VOLUME /data

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
    CMD ["/usr/local/bin/cudascope", "--mode=healthcheck"]

ENTRYPOINT ["cudascope"]
