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
| `FARO_DEVICE_CATALOG_PATH` | `/config/device-catalog.json` | Optional custom device-recognition catalog; the bundled catalog is used when this file does not exist |
| `FARO_IMAGE_NAMESPACE` | `tabierto` | Docker Hub image namespace |
| `FARO_VERSION` | `latest` | Faro image tag |

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
FARO_DEVICE_CATALOG_PATH=/config/device-catalog.json
FARO_VERSION=latest
```

The API remains internal to the Faro container and is accessed through its web server. It is not exposed as a separate host port. Use a nonstandard DNS port only for testing because routers normally cannot specify a port other than `53`.

Faro automatically accepts DNS clients from standard home and private networks while preventing an accidentally internet-exposed host from becoming an open resolver. Most installations need no configuration. Advanced networks using another routed IPv4 or IPv6 prefix can add it under **Settings → DNS & interface → Network access → Advanced**.

## Encrypted upstream DNS

Faro separates the connection your devices make to Faro from the connection Faro makes to a public DNS provider. Routers and devices continue using Faro normally on TCP or UDP port `53`. When **Encrypted** is selected on the **Upstreams** page, Faro sends public lookups from its local CoreDNS engine through an internal loopback gateway and then to the chosen providers with RFC 8484 DNS over HTTPS on port `443`.

New installations select **Encrypted — Recommended** during guided setup. Existing installations remain on **Standard DNS** after upgrading so a custom resolver is never replaced or bypassed unexpectedly. The Upstreams page always shows which mode is active.

Encrypted mode has these safety properties:

- TLS certificates are validated normally and TLS 1.2 or newer is required.
- Provider hostnames are bootstrapped with the public IPs selected in Faro, avoiding a circular dependency on Faro's own resolver.
- Health and latency checks use the actual HTTPS route rather than probing plaintext port `53`.
- If one encrypted provider is unavailable, Faro tries another selected encrypted provider.
- Faro never silently falls back from encrypted DNS to plaintext.

The built-in Cloudflare, Google Public DNS, Quad9, AdGuard DNS, and OpenDNS profiles have encrypted endpoints. Custom IP resolvers remain available in **Standard DNS** mode. Faro rejects an attempt to enable encryption while an unsupported custom resolver is selected and leaves the working DNS configuration in place.

## Deployment model

Faro always runs as one application container. The web interface, API, CoreDNS resolver, encrypted upstream gateway, and bounded query logger remain separate internal responsibilities, but they install, restart, and upgrade atomically. Standard Docker Compose and Unraid run the same `tabierto/faro` image; Unraid's XML file is only an installation template for that image. Development follows the same topology with Air and Vite added for hot reload.

Run the published image:

```sh
docker compose up -d
```

Build the same production container from a repository checkout:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

An installation created from Faro's older three-service preview uses a different volume layout. Create an encrypted Faro backup before replacing that Compose file, then restore the backup into the current container.

## Multi-server DNS redundancy

Faro can synchronize one primary server with any number of read-only DNS replicas. Every server runs the same single `tabierto/faro` container; there is no separate replica image or extra service. The primary remains the only place where settings are changed, while replicas answer DNS using the same accepted CoreDNS configuration.

To add a server:

1. Install Faro normally on another machine with a fixed LAN IP, but do not create its administrator account.
2. On the primary, open **Settings → Redundancy**, choose **Set up redundancy** or **Add DNS server**, and copy the temporary pairing code.
3. Open the fresh Faro server, choose **Join an existing Faro home**, and enter the primary's private Faro URL, pairing code, server name, and the new server's LAN IP.
4. Wait for both servers to show the same configuration revision, then advertise both LAN IPs as DNS servers through the router's DHCP settings.

The replica initiates every connection to the primary, so no inbound management port needs to be opened on the replica. Pairing codes expire after ten minutes and are single-use. Pairing uses an ephemeral X25519 key exchange; subsequent requests are timestamped, nonce-protected, and HMAC authenticated. Configuration snapshots are encrypted separately for each replica, and the long-lived node secrets are encrypted at rest.

Before accepting a revision, a replica stages the complete generated CoreDNS configuration, validates it with CoreDNS, replaces the live files atomically, and verifies that the running resolver accepted the reload. If validation or reload fails, it restores the previous runtime settings and DNS files. If the primary is unreachable, the replica keeps serving its last-known-good configuration and reports the synchronization problem on both status screens.

This first redundancy model intentionally keeps a single control plane:

- Configuration changes, blocklist downloads, protection edits, and administration happen only on the primary.
- Replicas are server-enforced read-only, not merely hidden by the interface.
- Query history and device observations remain local to the primary; they are not merged from replicas.
- A replica is not promoted automatically if the primary fails. It continues resolving DNS, but configuration changes wait until the primary returns.
- Portable database backups do not copy pairing secrets or cluster membership. Pair replicas again after restoring a primary onto a new installation.

This avoids split-brain configuration and makes DNS failover independent of database consensus. For predictable client failover, verify that the router distributes both DNS addresses; some consumer routers use only the first address until it stops responding, while others balance between them.

## Protection setups

**Home** is Faro's network default. Existing blocklist and exception choices are migrated into Home automatically. Use the separate **Blocklists** page to install, update, pause, or remove shared filtering sources. Under **Protection**, you can create additional named setups, choose an icon, select from those installed blocklists, add allow or block exceptions, and assign observed devices. Devices that are not assigned elsewhere always fall back to Home.

Device assignment uses the DNS client's source IP address. Give important devices stable DHCP leases so their protection remains predictable. If a router proxies every DNS request through its own address, Faro sees the router as one client and cannot apply different protection to the devices behind it.

## Device recognition catalog

Faro identifies devices from conservative combinations of device names, distinctive DNS domain families, and reserved local addresses. Recognition rules live in the versioned JSON catalog at `internal/devicecatalog/catalog.json`; they are not compiled into handler logic. Each automatic result stores the catalog version, score, and human-readable evidence that produced it. A manual device type always takes precedence.

Classification runs in bounded background batches rather than while the Devices page is loading. New devices and catalog revisions are handled first; active devices are reconsidered at most once every ten minutes when their DNS evidence changes. This keeps inventory reads fast even on busy networks.

To maintain the bundled catalog:

1. Add or adjust one narrowly scoped definition in `internal/devicecatalog/catalog.json` and increment `catalog_version`.
2. Prefer a distinctive vendor service domain over broad domains shared by browsers or apps.
3. Add a regression test for both the intended device and a plausible false positive.
4. Validate the file with:

   ```sh
   go run ./cmd/faro-device-catalog validate ./internal/devicecatalog/catalog.json
   ```

5. Run `go test ./internal/devicecatalog ./internal/api/handlers` before submitting the change.

Administrators can test their own catalog without rebuilding Faro by placing it at `/config/device-catalog.json`. Faro checks that file while running, accepts it only when its complete schema is valid, and keeps the last known-good catalog if a later edit is invalid. Delete the file to return to the bundled catalog. `GET /api/device-catalog` reports the active version, source, definition count, and any validation error.

## Device inventory performance

The Devices inventory is paginated in groups of 50 and supports database-backed search and sorting. Its list endpoint batches addresses, protection assignments, cached classifications, identity sources, and daily activity into a fixed number of database operations per page. Top-domain and replay data are loaded only after opening one device.

While the page is visible, Faro checks for changes every 15 seconds. Conditional requests return immediately when the inventory revision has not changed, superseded requests are cancelled, and a slow refresh cannot overlap the next one. The broader application refresh no longer reloads the complete device inventory every five seconds.

## UniFi Network integration

Faro can use the official local UniFi Network API to keep device identity stable when DHCP addresses change. Open **Settings → Integrations → UniFi Network**, enter the private HTTPS address of the UniFi console and a local Network API key created under **Control Plane → Integrations**, then choose the site to synchronize.

The integration is deliberately read-only. Faro imports the connected client's MAC address, current IP address, UniFi name, connection type, and uplink identifier. DNS activity remains Faro's source for traffic and filtering decisions; Faro does not change UniFi DHCP, DNS, firewall, network, or Wi-Fi configuration. Manually confirmed Faro names, icons, and protection assignments always take precedence over imported data.

Faro accepts only consoles that resolve to a private, loopback, link-local, or carrier-grade NAT address. A normally trusted HTTPS certificate is verified automatically. If the console uses a self-signed certificate, Faro displays its subject, expiration, and SHA-256 fingerprint for explicit review and pins that exact certificate after approval. A changed certificate stops synchronization until it is reviewed again.

The API key is encrypted with a random per-installation key stored beside Faro's database and is never returned to the browser or written to logs. Portable Faro backups deliberately exclude integration credentials and derived UniFi observations; reconnect UniFi after restoring onto a different installation. The synchronization runs once per minute and can also be started manually from the integration page.

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
docker compose port faro 53 --protocol udp
docker compose port faro 53 --protocol tcp
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

# Show recent application logs
docker compose logs --tail=200

# Pull and run the latest configured release
docker compose pull
docker compose up -d

# Stop Faro without deleting data
docker compose down
```

Do not run `docker compose down -v` unless you intend to permanently delete Faro's local data.

### Domain favicons

Favicon fetching is disabled by default because it makes outbound requests based on domains observed on the network. Enable **Settings → DNS behavior → Domain favicons** when you want icons. Faro first checks the queried hostname, then safely falls back to its registrable site domain and icons declared in the site's HTML. Downloaded files are validated, cached locally, and restricted to public HTTPS destinations. Failed lookups are retried after 15 minutes.

## Persistent data

Faro stores its database, generated resolver configuration, cached icons, and bounded raw query logs in the `faro-config` volume mounted at `/config`.

For routine Faro backups, open **Settings → Health & data → Encrypted backup & restore**. Faro downloads a portable `.faro-backup` file containing the SQLite database, including DNS settings, local records, rules, blocklists, account data, and retained history. The file is protected with a passphrase-derived key using Argon2id and AES-256-GCM.

Keep the backup passphrase separately: Faro cannot recover it. During restore, Faro retains a private snapshot of the current database until the generated configuration has been validated and the running CoreDNS instance accepts it. If DNS rejects the restored configuration, Faro restores the previous database and DNS configuration and reports that the restore failed; the UI cannot continue showing settings that DNS did not accept. A successful restore signs out every browser session. Active login sessions, integration credentials and derived router observations, cached favicon files, and the bounded raw query-log buffer are deliberately excluded; Faro recreates or re-synchronizes those operational files as needed.

Volume-level backups are still useful for full host disaster recovery, especially if you also want cached favicons and generated runtime files. Back up `faro-config` as part of the Docker host's normal backup process.

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

### Faro is healthy but DNS has no published port

First confirm that the rendered Compose configuration contains `published: "53"` for both protocols:

```sh
docker compose config
```

Then refresh the standalone Compose file and recreate Faro:

```sh
curl -fsSL https://raw.githubusercontent.com/derek-diaz/Faro/main/docker-compose.yml -o docker-compose.yml
docker compose up -d --pull always --force-recreate faro
docker compose port faro 53 --protocol udp
docker compose port faro 53 --protocol tcp
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
docker compose logs --tail=200
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

Development also runs as one container, matching the production and Unraid process topology. Open `http://localhost:1787`. Vite applies frontend edits with hot module replacement, while Air rebuilds and restarts the Go API after Go source changes. CoreDNS and the bounded query logger run alongside them under the same supervisor. Both source watchers use polling for reliable Windows and macOS bind-mount detection through Docker Desktop. Stop the stack with `Ctrl-C`, or run `make dev-down` from another terminal.

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

To build the production container from source:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

## Architecture

Faro uses a Go API, React and TypeScript frontend, SQLite database, and CoreDNS. Before replacing active files, the API starts the same CoreDNS binary against a private staged copy of the complete generated configuration. After replacement, it verifies CoreDNS's SHA-512 reload hash through the resolver's metrics endpoint. If the live resolver does not accept the new Corefile, Faro restores and verifies the previous configuration. CoreDNS handles local records, blocked domains, caching, forwarding into Faro's selected upstream transport, query logs, metrics, and configuration reloads. The Go process owns the loopback-only DNS-over-HTTPS gateway used by encrypted mode.

The application keeps four internal responsibilities:

- `api`: Faro's control plane, persistence, and configuration generation
- `ui`: the web application and reverse proxy for the API
- `dns`: CoreDNS resolution and filtering engine
- `encrypted upstream`: the loopback-only DNS-over-HTTPS gateway owned by the API process

The production and development images supervise those responsibilities together and expose only the web and DNS ports. If any required infrastructure process exits, the container stops so Docker can restart the complete application. In development, Air and Vite retain backend and frontend hot reload inside that same topology.

## Publishing Docker releases

Repository maintainers need this Docker Hub repository in the target namespace:

- `faro`

Configure a GitHub environment named `Faro CI` with:

- Secret `DOCKERHUB_TOKEN`: a Docker Hub access token with write access
- Optional variable `DOCKERHUB_USERNAME`: login account, defaults to `tabierto`
- Optional variable `DOCKERHUB_NAMESPACE`: image namespace, defaults to `tabierto`

Push a semantic version tag to publish the Faro image:

```sh
git tag v1.0.0
git push origin v1.0.0
```

The workflow publishes `latest`, the full version such as `1.0.0`, and the minor version such as `1.0` for `linux/amd64` and `linux/arm64`.
