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
# Which build this is, for the dashboard footer. .git is not in the build
# context, so CI passes them in; left empty, the footer says "dev".
ARG VERSION=
ARG REVISION=
ARG BUILD_TIME=
RUN go build -ldflags="-s -w \
    -X github.com/sergey/cudascope/internal/buildinfo.Version=${VERSION} \
    -X github.com/sergey/cudascope/internal/buildinfo.Revision=${REVISION} \
    -X github.com/sergey/cudascope/internal/buildinfo.BuildTime=${BUILD_TIME}" \
    -o cudascope ./cmd/cudascope/

# Stage 3: Runtime
# No CUDA base needed — NVIDIA Container Toolkit mounts libnvidia-ml.so from the host.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Tell NVIDIA Container Toolkit to mount the driver libraries
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=utility

COPY --from=go-builder /app/cudascope /usr/local/bin/cudascope

EXPOSE 9090
VOLUME /data

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s \
    CMD ["/usr/local/bin/cudascope", "--mode=healthcheck"]

ENTRYPOINT ["cudascope"]
