<p align="center">
  <img src="frontend/public/logos/web/icon-512.png" alt="Faro lighthouse logo" width="128" />
</p>

<h1 align="center">Faro</h1>

<p align="center">
  Friendly, self-hosted DNS control and network visibility for your homelab.
</p>

Faro runs CoreDNS behind an approachable web interface. It shows what devices are requesting, explains why a domain was allowed or blocked, manages local DNS and blocklists, compares upstream resolvers, and keeps all configuration and activity on your own network.

## Quick start

You need Docker with Docker Compose and a machine with a fixed LAN IP or DHCP reservation.

```sh
git clone https://github.com/derek-diaz/Faro.git
cd Faro
docker compose up -d --build
```

Open `http://YOUR-FARO-IP:1787` and complete the guided setup. Faro will ask you to:

1. Create the local administrator account.
2. Confirm the Faro machine's LAN IP.
3. Choose upstream DNS providers.
4. Optionally install a starter blocklist.

When setup is complete, configure your router's DHCP DNS server to use `YOUR-FARO-IP`.

> Faro publishes DNS on TCP and UDP port `53`. Most routers require this standard port. Make sure another DNS service is not already using port 53 on the Docker host.

## Published ports

| Port | Protocol | Purpose |
| --- | --- | --- |
| `1787` | TCP | Faro web interface, health check, and metrics |
| `53` | TCP and UDP | DNS for your router and devices |

The API stays inside the Compose network and is accessed through the web container. It is not exposed as a separate host port.

Faro uses `1787` as its default web port, a small nod to Puerto Rico's `+1 787` numbering. You can replace it with any available TCP port.

To change a published port, copy the example environment file and edit it before starting Faro:

```sh
cp .env.example .env
```

```dotenv
FARO_BIND_ADDRESS=0.0.0.0
FARO_UI_PORT=1787
FARO_DNS_PORT=53
```

Use a nonstandard DNS port only for local testing. Routers normally cannot specify a DNS port other than `53`.

## Verify the installation

Check the containers:

```sh
docker compose ps
```

Test DNS from another machine on the network:

```sh
nslookup example.com YOUR-FARO-IP
```

Useful URLs:

- Faro: `http://YOUR-FARO-IP:1787`
- Health: `http://YOUR-FARO-IP:1787/healthz`
- Prometheus metrics: `http://YOUR-FARO-IP:1787/metrics`

## Everyday commands

```sh
# Follow logs
docker compose logs -f

# Apply an update after pulling new code
docker compose up -d --build

# Stop Faro without deleting data
docker compose down
```

Faro stores its database, generated CoreDNS configuration, and DNS logs in named Docker volumes. `docker compose down` preserves them. Running `docker compose down -v` permanently deletes Faro's local data.

## What Faro includes

- First-run onboarding and local administrator authentication
- Dashboard and searchable DNS activity
- Device inventory, friendly names, and activity replay
- Local DNS records and per-domain allow rules
- Curated and custom blocklists
- Upstream DNS selection with live latency checks
- DNS cache and upstream-resolution visibility
- Domain decision details: why Faro allowed or blocked a request
- Configurable retention, database pruning, and health metrics

Faro does not add fake activity. Dashboards remain empty until a device sends DNS requests through Faro.

## Troubleshooting

**Port 53 is already in use**

Stop the existing DNS service or set `FARO_DNS_PORT=1053` in `.env` for testing. Router-wide DNS still requires port `53`.

**The UI opens but no activity appears**

Confirm the client or router is using the Faro host IP as its DNS server. Queries sent to another resolver cannot appear in Faro.

**A container is unhealthy**

```sh
docker compose logs --tail=200 faro-api faro-ui coredns
```

## Local development

```sh
# Backend
go run ./cmd/faro-api

# Frontend
cd frontend
npm install
npm run dev
```

The frontend development server proxies `/api`, `/healthz`, and `/metrics` to the Go API on port `8080`.

<details>
<summary>Architecture notes</summary>

Faro uses a Go API, React and TypeScript frontend, SQLite database, and CoreDNS. The API generates CoreDNS configuration into a shared volume and replaces active files safely. CoreDNS handles local records, blocked domains, caching, upstream forwarding, query logs, metrics, and configuration reloads.

The persistent volumes are:

- `faro-data`: SQLite database and cached domain icons
- `coredns-config`: generated CoreDNS files
- `coredns-logs`: DNS query logs consumed by Faro

</details>

<p align="center">
  Made in Puerto Rico.
</p>
