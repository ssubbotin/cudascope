# CudaScope

Lightweight, self-hosted NVIDIA GPU monitoring with real-time dashboards and historical metrics.

- **Direct NVML access** via [go-nvml](https://github.com/NVIDIA/go-nvml) - no nvidia-smi parsing
- **Embedded storage** - SQLite with automatic rollup retention (raw 1s -> 1m -> 1h)
- **Single binary** - Go backend with embedded Svelte 5 SPA (go:embed)
- **Zero dependencies** - no Prometheus, no Grafana, no InfluxDB
- **Multi-node** - Docker Swarm support with agent/hub architecture
- **1-command install** - `docker compose up -d`

## Quick Start

### Standalone (single host)

```bash
docker compose up -d
```

Open [http://localhost:9090](http://localhost:9090).

Or without Compose:

```bash
docker run -d --gpus all -p 9090:9090 -v cudascope-data:/data cudascope/cudascope
```

### Docker Swarm (multi-node GPU cluster)

Label your GPU nodes:

```bash
docker node update --label-add gpu=true <node-name>
```

Deploy the stack:

```bash
docker stack deploy -c docker-stack.yml cudascope
```

This deploys an **agent** on every GPU node (global mode) and a single **hub** on the manager node.

## Configuration

All settings via environment variables or CLI flags:

| Variable | Flag | Default | Description |
|----------|------|---------|-------------|
| `CUDASCOPE_MODE` | `--mode` | `standalone` | `standalone`, `hub`, or `agent` |
| `CUDASCOPE_PORT` | `--port` | `9090` | HTTP listen port |
| `CUDASCOPE_DATA_DIR` | `--data-dir` | `/data` | SQLite database location |
| `CUDASCOPE_HUB_URL` | `--hub-url` | - | Hub URL (agent mode only) |
| `CUDASCOPE_NODE_ID` | `--node-id` | hostname | Node identifier for multi-node |
| `CUDASCOPE_COLLECT_INTERVAL` | `--collect-interval` | `1s` | GPU metric collection interval |
| `CUDASCOPE_HOST_INTERVAL` | `--host-interval` | `5s` | Host metric collection interval |
| `CUDASCOPE_RETENTION_RAW` | `--retention-raw` | `24h` | Raw metrics retention |
| `CUDASCOPE_RETENTION_1M` | `--retention-1m` | `720h` | 1-minute rollup retention (30d) |
| `CUDASCOPE_RETENTION_1H` | `--retention-1h` | `8760h` | 1-hour rollup retention (365d) |
| `CUDASCOPE_AUTH` | `--auth` | - | Basic auth `user:password` |
| `CUDASCOPE_ALERT_TEMP` | `--alert-temp` | `0` | Temperature alert threshold (C) |
| `CUDASCOPE_ALERT_GPU_UTIL` | `--alert-gpu-util` | `0` | GPU utilization alert (%) |
| `CUDASCOPE_ALERT_MEM_UTIL` | `--alert-mem-util` | `0` | Memory utilization alert (%) |

Alert thresholds of `0` mean disabled.

## Features

### Dashboard

- Per-GPU cards with real-time utilization, VRAM, temperature, fan, power, sparklines
- Host card with CPU, RAM, disk, network
- Multi-GPU overlay charts (utilization, memory)
- Host CPU and RAM history charts
- GPU process list with VRAM usage

### GPU Detail Page

Click any GPU card for full-screen charts:

- Utilization (GPU + memory %)
- Memory usage (MiB)
- Temperature and fan speed
- Power draw (W)
- Clock speeds (graphics + memory MHz)
- PCIe throughput (TX/RX KB/s)
- Encoder / decoder utilization
- Process list

All charts support synchronized crosshairs and configurable time ranges.

### Multi-Node

When running in Swarm mode:

- Node selector to filter by node or view aggregate
- Online/offline node health indicators (60s heartbeat threshold)
- Per-node labels on charts and GPU cards
- Node column in process list

### Alerts

Set thresholds via config. When exceeded:

- Alert count badge in navbar
- Red border and warning icon on affected GPU cards
- Alert details via `/api/v1/alerts`

### Prometheus

Expose metrics for existing monitoring stacks:

```
GET /metrics
```

Returns all GPU and host metrics in Prometheus text exposition format with labels `node_id`, `gpu_id`, `gpu_name`.

### Authentication

Enable basic auth:

```bash
docker run -d --gpus all -p 9090:9090 \
  -e CUDASCOPE_AUTH=admin:secret \
  -v cudascope-data:/data cudascope/cudascope
```

Protects all endpoints except `/api/v1/healthz` and agent ingest routes.

### Themes

Dark, light, and system-preference themes. Toggle via the navbar icon.

### Time Ranges

Preset ranges: 5m, 15m, 1h, 6h, 24h. Auto-refresh toggle and manual refresh button. Data automatically uses the best resolution tier (raw/1m/1h) based on the time span.

## API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/status` | GET | Current snapshot (GPUs, hosts, devices, processes, alerts, nodes) |
| `/api/v1/nodes` | GET | List registered nodes with online status |
| `/api/v1/gpus` | GET | List GPU devices |
| `/api/v1/gpus/:id/metrics?range=5m` | GET | Historical GPU metrics |
| `/api/v1/gpus/:id/processes` | GET | Current GPU processes |
| `/api/v1/host/metrics?range=5m` | GET | Historical host metrics |
| `/api/v1/alerts` | GET | Active alerts and config |
| `/api/v1/ws` | WS | Real-time metric stream |
| `/api/v1/healthz` | GET | Health check |
| `/metrics` | GET | Prometheus exposition |

Query parameters: `?range=5m`, `?from=&to=` (unix timestamps), `?node=` (filter by node).

## Architecture

```
Standalone:  Collector -> SQLite -> HTTP/WS -> Browser

Swarm:       Agent (per node)  --HTTP POST-->  Hub  -> SQLite -> HTTP/WS -> Browser
             [go-nvml + gopsutil]               [storage + API + UI]
```

### Modes

- **standalone** (default): Collects GPU/host metrics locally, stores in SQLite, serves UI
- **hub**: Receives metrics from agents, stores, serves UI. No local GPU access needed
- **agent**: Collects GPU/host metrics, pushes to hub via HTTP. Minimal footprint

## Tech Stack

| Layer | Technology |
|-------|-----------|
| Backend | Go 1.24, net/http, gorilla/websocket |
| GPU metrics | go-nvml (NVIDIA/go-nvml) |
| Host metrics | gopsutil/v4 |
| Storage | SQLite (modernc.org/sqlite, pure Go) |
| Frontend | Svelte 5, SvelteKit, adapter-static |
| Charts | uPlot |
| Styling | Tailwind CSS v4 |
| Runtime | nvidia/cuda:12.8.0-base-ubuntu24.04 |

## Building from Source

Prerequisites: Go 1.22+, Node.js 22+, NVIDIA GPU with drivers installed.

```bash
# Build frontend
cd ui && npm ci && npm run build && cd ..

# Build binary
go build -o cudascope ./cmd/cudascope/

# Run (requires NVML library)
./cudascope --data-dir ./data
```

### Docker Build

```bash
docker compose build
```

## Data Retention

| Tier | Resolution | Retention | Size (1 GPU) |
|------|-----------|-----------|-------------|
| Raw | 1s | 24h | ~8 MB/day |
| 1-minute | 1m avg | 30d | ~4 MB/month |
| 1-hour | 1h avg | 365d | ~1 MB/year |

Rollup and pruning run automatically every 60 seconds.

## License

MIT
