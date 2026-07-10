# Faro

Faro is a self-hosted DNS control plane for modern homelabs. It manages CoreDNS underneath it and provides a UI, API, SQLite configuration model, blocklist management, query visibility, and a conservative CoreDNS reload workflow.

This repository is an MVP skeleton. It is intentionally small, local-first, and built around Docker Compose.

## Stack

- Backend: Go REST API
- Frontend: React + TypeScript + Vite
- DNS engine: CoreDNS
- Database: SQLite
- Runtime: Docker Compose

## Run with Docker Compose

```sh
docker compose up -d
```

Then open:

- Faro UI: http://localhost:3000
- API health: http://localhost:8080/healthz
- Metrics: http://localhost:8080/metrics
- DNS listener: your Docker host IP on port `53` by default

The default DNS port is `53` because routers such as UniFi normally let you set only a DNS server IP, not a custom port. If you are testing locally and port 53 is already in use, set `FARO_DNS_PORT=1053` in `.env` and query Faro explicitly on that port.

## Local development

Backend:

```sh
go run ./cmd/faro-api
```

Frontend:

```sh
cd frontend
npm install
npm run dev
```

The Vite dev server proxies `/api`, `/healthz`, and `/metrics` to `localhost:8080`.

## Demo data

Faro seeds useful configuration data by default: sample local records, a sample manual blocklist, and a sample blocklist source. It does not seed fake query traffic by default. Query charts and tables stay empty until CoreDNS actually receives queries.

If you want demo query rows for screenshots or UI development, start the API with:

```sh
FARO_SEED_DEMO_QUERIES=true
```

## What the MVP includes

- Health check at `/healthz`
- Prometheus metrics at `/metrics`
- CRUD API for local DNS records
- Blocklist CRUD plus refresh/download
- Allowlist and manual blocklist entries
- Query log API and dashboard summary API
- SQLite migrations and seed data
- CoreDNS Corefile generation from SQLite state
- Generated `local.hosts` and `blocklist.hosts`
- Safe file replacement with rollback if generated CoreDNS files cannot be written
- CoreDNS log tailer that parses query logs into SQLite
- React UI pages for Dashboard, Activity, Devices, Local DNS, Blocklists, Allowlist / Blocklist, and Settings
- Favicon fetching for public-looking domains when enabled, with local placeholder circles for local-only names

## DNS behavior

Faro generates CoreDNS configuration files into a shared Docker volume:

- `Corefile`
- `faro.hosts`
- `local.hosts`
- `blocklist.hosts`

Local `A` and `AAAA` records are rendered into `local.hosts`. Blocklist and manual blocked domains are rendered into `blocklist.hosts` as `0.0.0.0 domain`. CoreDNS reads the combined `faro.hosts` file because CoreDNS only allows one `hosts` plugin per server block. Allowlist entries are excluded from generated block entries.

The Corefile uses:

- `hosts` for local records
- `hosts` for blocked domains
- `forward` for upstream DNS, defaulting to `1.1.1.1` and `9.9.9.9`
- `log` and `reload`

For MVP validation, Faro performs basic generated Corefile checks and writes files through temp files before replacing active files. The reload plugin in CoreDNS picks up changes. A future iteration should run the CoreDNS binary against the generated Corefile before replacement when that binary is available in the API container.

## Blocklist formats

Faro parses common host/blocklist lines:

```txt
0.0.0.0 domain.com
127.0.0.1 domain.com
domain.com
# comments
! comments
```

Domains are normalized and deduplicated.

## Point a client or router at Faro

For a single client, set its DNS server to the host running Faro:

- DNS server: your Docker host IP
- Port: `53` for the default config

Most routers only support DNS on port 53. For router-wide DNS, keep `FARO_DNS_PORT=53`, restart Docker Compose, then configure the UniFi DHCP DNS server to the host IP running Faro. Use `1053` only for local testing where you can specify a DNS port manually.

## API overview

- `GET /healthz`
- `GET /api/dns-records`
- `POST /api/dns-records`
- `PUT /api/dns-records/{id}`
- `DELETE /api/dns-records/{id}`
- `GET /api/blocklists`
- `POST /api/blocklists`
- `PUT /api/blocklists/{id}`
- `POST /api/blocklists/{id}/refresh`
- `DELETE /api/blocklists/{id}`
- `GET /api/allowlist`
- `POST /api/allowlist`
- `DELETE /api/allowlist/{id}`
- `GET /api/blocklist-domains`
- `POST /api/blocklist-domains`
- `DELETE /api/blocklist-domains/{id}`
- `GET /api/queries`
- `GET /api/dashboard`
- `GET /api/settings`
- `PUT /api/settings`
- `POST /api/reload`
- `GET /metrics`

## Not included yet

- DHCP
- DNS over HTTPS or DNS over TLS
- Multi-user auth
- Kubernetes deployment
- Complex IPAM
