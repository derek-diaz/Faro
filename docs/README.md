# Faro technical and deployment guide

This guide contains the operational and development details intentionally omitted from the [main README](../README.md).

## Configuration

Faro reads optional deployment settings from a `.env` file beside `docker-compose.yml`. Start with the provided example when using a repository checkout, or create the file manually for a standalone deployment.

| Variable | Default | Purpose |
| --- | --- | --- |
| `FARO_BIND_ADDRESS` | `0.0.0.0` | Host address used for published ports |
| `FARO_UI_PORT` | `1787` | Faro web interface port |
| `FARO_DNS_PORT` | `53` | DNS port published over TCP and UDP |
| `FARO_DEV_DNS_PORT` | `5354` | Host DNS port used by `docker-compose.dev.yml` |
| `FARO_QUERY_LOG_MAX_BYTES` | `10485760` | Maximum size of each raw CoreDNS query-log file |
| `FARO_QUERY_LOG_BACKUPS` | `2` | Number of rotated raw query-log files retained |
| `FARO_DOCKER_LOG_MAX_SIZE` | `10m` | Maximum size of each Docker stdout/stderr log file |
| `FARO_DOCKER_LOG_BACKUPS` | `3` | Number of rotated Docker log files retained per container |
| `FARO_IMAGE_NAMESPACE` | `tabierto` | Docker Hub image namespace |
| `FARO_VERSION` | `latest` | Image tag used by all Faro services |

Example:

```dotenv
FARO_BIND_ADDRESS=0.0.0.0
FARO_UI_PORT=1787
FARO_DNS_PORT=53
FARO_DEV_DNS_PORT=5354
FARO_QUERY_LOG_MAX_BYTES=10485760
FARO_QUERY_LOG_BACKUPS=2
FARO_DOCKER_LOG_MAX_SIZE=10m
FARO_DOCKER_LOG_BACKUPS=3
FARO_VERSION=latest
```

The API remains inside the Compose network and is accessed through the web container. It is not exposed as a separate host port. Use a nonstandard DNS port only for testing because routers normally cannot specify a port other than `53`.

## Verify the installation

Check container health:

```sh
docker compose ps
```

Test DNS from another machine on the network:

```sh
nslookup example.com YOUR-FARO-IP
```

Confirm that Docker published both DNS protocols:

```sh
docker compose port dns 53 --protocol udp
docker compose port dns 53 --protocol tcp
```

Both commands should report the Faro host on port `53`.

Useful endpoints:

- Web interface: `http://YOUR-FARO-IP:1787`
- Health check: `http://YOUR-FARO-IP:1787/healthz`
- Prometheus metrics: `http://YOUR-FARO-IP:1787/metrics`

## Operations

```sh
# Follow all logs
docker compose logs -f

# Show recent service logs
docker compose logs --tail=200 api ui dns

# Pull and run the latest configured release
docker compose pull
docker compose up -d

# Stop Faro without deleting data
docker compose down
```

Do not run `docker compose down -v` unless you intend to permanently delete Faro's local data.

## Persistent data

Faro stores state in named Docker volumes:

- `faro-data`: SQLite database and cached domain icons
- `coredns-config`: generated CoreDNS configuration
- `coredns-logs`: bounded DNS query-log buffer consumed by Faro; retained history is stored in `faro-data`

For routine Faro backups, open **Settings → Health & data → Encrypted backup & restore**. Faro downloads a portable `.faro-backup` file containing the SQLite database, including DNS settings, local records, rules, blocklists, account data, and retained history. The file is protected with a passphrase-derived key using Argon2id and AES-256-GCM.

Keep the backup passphrase separately: Faro cannot recover it. Restoring replaces the live database atomically, reloads CoreDNS, and signs out every browser session. Active login sessions, cached favicon files, and the bounded raw query-log buffer are deliberately excluded; Faro recreates those operational files as needed.

Volume-level backups are still useful for full host disaster recovery, especially if you also want cached favicons and generated runtime files. Back up the three volumes above as part of the Docker host's normal backup process.

## Troubleshooting

### Docker reports `no space left on device`

Check Docker's virtual-disk usage rather than only the Faro volumes:

```sh
docker system df -v
```

Faro bounds both its raw query-log volume and the stdout/stderr logs captured by Docker. It also automatically reclaims legacy oversized query-log files. If Docker's overall disk is full, Faro keeps DNS running and temporarily pauses raw query-log persistence until space becomes available. Log-driver limits apply when containers are created, so recreate older Faro containers after upgrading. Reclaim unused Docker build cache with `docker builder prune` or increase Docker Desktop's virtual-disk limit. Review the prune prompt carefully because build cache is shared by every local project.

### Port 53 is already in use

Identify the listener first:

```sh
sudo ss -lntup '( sport = :53 )'
```

If `systemd-resolved` is listening only on a loopback address such as `127.0.0.53`, keep it running and bind Faro to the server's fixed LAN IP in `.env`:

```dotenv
FARO_BIND_ADDRESS=192.168.1.10
FARO_DNS_PORT=53
```

Apply the binding by recreating the services:

```sh
docker compose up -d --force-recreate
```

If the listener is a leftover Technitium system installation, its standard service is `dns.service`. Stop it only after confirming that it is the process shown above:

```sh
sudo systemctl disable --now dns.service
docker compose up -d --force-recreate
```

On macOS, Internet Sharing and DNS-filtering apps may own port `53`. Identify a privileged listener with:

```sh
sudo lsof -nP -iTCP:53 -sTCP:LISTEN
sudo lsof -nP -iUDP:53
```

Development Compose publishes DNS on port `5354` by default, so test it with `dig @127.0.0.1 -p 5354 example.com`. Override `FARO_DEV_DNS_PORT` if needed. Production and router-wide DNS still require the standard port `53`.

### The DNS container is healthy but has no published port

First confirm that the rendered Compose configuration contains `published: "53"` for both protocols:

```sh
docker compose config
```

Then refresh the standalone Compose file and recreate only the DNS container:

```sh
curl -fsSL https://raw.githubusercontent.com/derek-diaz/Faro/main/docker-compose.yml -o docker-compose.yml
docker compose up -d --pull always --force-recreate dns
docker compose port dns 53 --protocol udp
docker compose port dns 53 --protocol tcp
```

If recreation reports that port `53` is already allocated, identify the host DNS service before changing it:

```sh
sudo ss -lntup '( sport = :53 )'
```

### The UI opens but no activity appears

Confirm the client or router is using the Faro host's LAN IP as its DNS server. Queries sent directly to another resolver cannot appear in Faro.

### A container is unhealthy

Inspect status and logs:

```sh
docker compose ps
docker compose logs --tail=200 api ui dns
```

## Local development

Local frontend development requires Node.js 24. The repository includes `.nvmrc` and `.node-version` files for compatible version managers.

Clone the repository:

```sh
git clone https://github.com/derek-diaz/Faro.git
cd Faro
```

To run the full development stack in containers with hot reload:

```sh
docker compose -f docker-compose.dev.yml up --build
```

Open `http://localhost:1787`. Vite applies frontend edits with hot module replacement, while Air rebuilds and restarts the Go API after Go source changes. The Compose file uses polling for reliable file watching through Docker Desktop bind mounts. Stop the stack with `Ctrl-C`, or run `make dev-down` from another terminal.

Development DNS is published on host port `5354` by default to avoid privileged/system DNS listeners. Test it with `dig @127.0.0.1 -p 5354 example.com`, or set `FARO_DEV_DNS_PORT` to another available port. You can also use `make dev` as a shortcut for the command above.

To run the API and frontend directly on the host instead, use the following commands.

Run the API:

```sh
go run ./cmd/faro-api
```

Run the frontend in another terminal:

```sh
cd frontend
npm install
npm run dev
```

The frontend development server proxies `/api`, `/healthz`, and `/metrics` to the Go API on port `8080`.

To build all containers from source:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

## Architecture

Faro uses a Go API, React and TypeScript frontend, SQLite database, and CoreDNS. The API generates CoreDNS configuration into a shared volume and safely replaces active files. CoreDNS handles local records, blocked domains, caching, upstream forwarding, query logs, metrics, and configuration reloads.

The Compose application contains three services:

- `api`: Faro's control plane, persistence, and configuration generation
- `ui`: the web application and reverse proxy for the API
- `dns`: CoreDNS resolution and filtering engine

## Publishing Docker releases

Repository maintainers need these Docker Hub repositories in the target namespace:

- `faro-api`
- `faro-ui`
- `faro-dns`

Configure a GitHub environment named `Faro CI` with:

- Secret `DOCKERHUB_TOKEN`: a Docker Hub access token with write access
- Optional variable `DOCKERHUB_USERNAME`: login account, defaults to `tabierto`
- Optional variable `DOCKERHUB_NAMESPACE`: image namespace, defaults to `tabierto`

Push a semantic version tag to publish all three images:

```sh
git tag v1.0.0
git push origin v1.0.0
```

The workflow publishes `latest`, the full version such as `1.0.0`, and the minor version such as `1.0` for `linux/amd64` and `linux/arm64`.
