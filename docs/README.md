# Faro technical and deployment guide

This guide contains the operational and development details intentionally omitted from the [main README](../README.md).

## Configuration

Faro reads optional deployment settings from a `.env` file beside `docker-compose.yml`. Start with the provided example when using a repository checkout, or create the file manually for a standalone deployment.

| Variable | Default | Purpose |
| --- | --- | --- |
| `FARO_BIND_ADDRESS` | `0.0.0.0` | Host address used for published ports |
| `FARO_UI_PORT` | `1787` | Faro web interface port |
| `FARO_DNS_PORT` | `53` | DNS port published over TCP and UDP |
| `FARO_IMAGE_NAMESPACE` | `tabierto` | Docker Hub image namespace |
| `FARO_VERSION` | `latest` | Image tag used by all Faro services |

Example:

```dotenv
FARO_BIND_ADDRESS=0.0.0.0
FARO_UI_PORT=1787
FARO_DNS_PORT=53
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
- `coredns-logs`: DNS query logs consumed by Faro

Back up these volumes as part of the Docker host's normal backup process.

## Troubleshooting

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

For local testing only, `FARO_DNS_PORT=1053` avoids the conflict. Router-wide DNS normally requires port `53`.

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
