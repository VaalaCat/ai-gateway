# AI Gateway

A distributed AI API gateway with a control-plane/data-plane architecture. Provides OpenAI-compatible `/v1/*` relay endpoints, built-in management APIs, Web UI, and single-binary deployment.

[中文文档](README.zh.md)

## Features

- **Control Plane Management** — Users, tokens, channels, models, and agents
- **Data Plane Relay** — OpenAI-compatible API endpoints (`/v1/chat/completions`, `/v1/responses`, etc.)
- **Real-Time Config Sync** — Master/agent incremental sync over WebSocket
- **Quota & Billing** — Usage-based settlement and quota enforcement
- **Model Routing** — Aggregate multiple upstream models under one name with priority/weight load balancing
- **Single Binary** — Frontend static assets embedded; no separate web server needed

## Screenshots

<table>
  <tr>
    <td colspan="2"><a href="docs/images/en/dashboard.png"><img src="docs/images/en/dashboard.png" alt="Dashboard"/></a></td>
  </tr>
  <tr>
    <td width="50%"><a href="docs/images/en/channels.png"><img src="docs/images/en/channels.png" alt="Channels"/></a><br/><sub><b>Channels</b> — upstream provider configuration</sub></td>
    <td width="50%"><a href="docs/images/en/models.png"><img src="docs/images/en/models.png" alt="Models"/></a><br/><sub><b>Models</b> — per-model pricing</sub></td>
  </tr>
  <tr>
    <td><a href="docs/images/en/model-routings.png"><img src="docs/images/en/model-routings.png" alt="Model Routings"/></a><br/><sub><b>Model Routings</b> — priority/weight aggregation</sub></td>
    <td><a href="docs/images/en/logs.png"><img src="docs/images/en/logs.png" alt="Usage Logs"/></a><br/><sub><b>Usage Logs</b> — per-request audit trail</sub></td>
  </tr>
  <tr>
    <td><a href="docs/images/en/billing.png"><img src="docs/images/en/billing.png" alt="Billing"/></a><br/><sub><b>Billing</b> — daily rollups by token and channel</sub></td>
    <td><a href="docs/images/en/playground.png"><img src="docs/images/en/playground.png" alt="Playground"/></a><br/><sub><b>Playground</b> — in-browser chat tester</sub></td>
  </tr>
</table>

[See all 20 screenshots →](docs/screenshots.md)

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                   master (control plane)             │
│  ┌──────────┐  ┌──────────┐  ┌───────────────────┐ │
│  │ Admin API│  │  Web UI  │  │ Agent Sync Hub    │ │
│  │ & Auth   │  │ (embed)  │  │ (WebSocket)       │ │
│  └──────────┘  └──────────┘  └───────────────────┘ │
│  ┌──────────────────────────────────────────────┐   │
│  │         Billing & Quota Settlement           │   │
│  └──────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
          │ WebSocket sync
          ▼
┌─────────────────────────────────────────────────────┐
│                   agent (data plane)                 │
│  ┌──────────────┐  ┌────────────┐  ┌────────────┐  │
│  │ /v1/* Relay  │  │ Token/Chan │  │  Usage     │  │
│  │ Endpoints    │  │ Cache      │  │  Reporter  │  │
│  └──────────────┘  └────────────┘  └────────────┘  │
└─────────────────────────────────────────────────────┘
```

### Deployment Topologies

| Topology | Pros | Cons | Use Case |
|----------|------|------|----------|
| Single node (master + embedded agent) | Simplest setup; one container | Shared resources; single point of failure | PoC, testing, small production |
| Multi-node (master + external agents) | Horizontal scaling; fault isolation; geo-distribution | Higher ops complexity; enrollment lifecycle | Medium/large production, multi-region |

## Quick Start

```bash
# 1. Prepare config
mkdir -p deploy data
cp config.example.yaml deploy/config.yaml
# Edit deploy/config.yaml — set jwt_secret and admin_password

# 2. Run with Docker Compose
export AI_GATEWAY_IMAGE=ghcr.io/vaalacat/ai-gateway:latest
docker compose up -d

# 3. Access
# Web UI: http://localhost:8140
# Health: http://localhost:8140/ping
```

## Configuration

The configuration file accepts these top-level keys:

- `log_level` — Logging verbosity (debug, info, warn, error)
- `master` — Control plane settings (listen address, DB, JWT, admin credentials)
- `agent` — Data plane settings (listen address, master URL, enrollment)
- `runtime` — Optional advanced tuning (timeouts, heartbeat, retry)

See [`config.example.yaml`](config.example.yaml) for a complete template.

## Deployment

### Single Node (Docker Compose)

See the [Quick Start](#quick-start) section above. Full details in [`docker-compose.yml`](docker-compose.yml).

### Multi-Node (External Agents)

1. Generate an enrollment token from master
2. Configure agent with `master_url` and `enrollment_token`
3. Start with `docker compose -f docker-compose.yml -f docker-compose.agent.yml up -d`

See [`docker-compose.agent.yml`](docker-compose.agent.yml) for the overlay template.

### Kubernetes

See [`docs/k8s-deployment.md`](docs/k8s-deployment.md) for Kubernetes deployment guidance.

## Development

```bash
# Prerequisites: Go 1.25+, Node.js 20+, pnpm

# Build (frontend + backend)
CGO_ENABLED=0 bash ./build.sh

# Run tests
CGO_ENABLED=0 go test ./... -count=1 -timeout=120s

# Frontend dev server (port 8141, proxies to :8140)
cd web && pnpm install && pnpm dev
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, code style, and PR process.

## Acknowledgments

This project stands on the shoulders of:

- **[new-api](https://github.com/QuantumNous/new-api)** by [@QuantumNous](https://github.com/QuantumNous) — the legacy channel adaptor, 50+ upstream provider constants, model-fetch protocols, and token-counting utilities are reused via `github.com/QuantumNous/new-api`. Without this prior work, out-of-the-box support for 50+ providers would not be feasible. Sincere thanks to the new-api maintainers and contributors.

## License

[MIT](LICENSE)
