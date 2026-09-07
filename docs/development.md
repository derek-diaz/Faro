# Run and test Faro from source

[All guides](README.md) · Related: [Architecture](architecture.md) · [Publish a release](releasing.md)

Use the development container for a complete local Faro installation with automatic reloads. It includes the API, interface, DNS engine, and supporting processes.

## 1. Get the source

Install Git and Docker with Compose, then run:

```sh
git clone https://github.com/derek-diaz/Faro.git
cd Faro
```

If production Faro already uses port 1787 on this machine, create `.env` in the checkout and choose another interface port:

```dotenv
FARO_UI_PORT=1788
```

Development DNS uses port **5354** by default. Keep your router pointed at the working production server while developing.

## 2. Start the development stack

```sh
docker compose -f docker-compose.dev.yml up --build
```

Open `http://localhost:1787`, or your chosen port, and complete setup. Frontend edits reload through Vite; Go API edits rebuild through Air. The development Compose project has its own data volumes.

Development paths differ from production: database and secrets are under `/data`, resolver files under `/coredns`, and raw DNS logs under `/var/log/coredns`.

## 3. Test DNS

If `dig` is installed:

```sh
dig @127.0.0.1 -p 5354 example.com
```

With Go installed, the repository includes a tester:

```sh
go run ./cmd/faro-dns-test -port 5354
```

Check the answer and find the request in Faro. `nslookup example.com localhost` uses the standard port 53, so it does not test this development mapping.

## 4. Run checks for your changes

For host-side checks, install the Go version required by [go.mod](../go.mod) or newer, a C compiler for SQLite's CGO driver, and Node.js 24. The checkout includes `.nvmrc` and `.node-version`.

Backend:

```sh
go test ./...
go vet ./...
```

Frontend, from the repository root:

```sh
cd frontend
npm ci
npm run build
node --test tests/favicons.test.mjs
```

Return to the repository root with `cd ..` before running Go or Docker commands. For a frontend-only server, `npm run dev` uses the API target in `VITE_API_PROXY_TARGET` or defaults to `http://localhost:8080`; it does not start a DNS server.

Choose checks that exercise your change. For DNS availability changes, the disposable production-container smoke check requires Docker, Bash, and either `dig` or Node.js:

```sh
bash tools/dns-reliability.sh
```

For history-query performance changes, run:

```sh
go test ./internal/api/handlers -run '^$' -bench BenchmarkHistoryReads -benchtime=3x -count=1
```

That benchmark measures local handler/database work, not browser speed or real-world network latency.

## 5. Stop the stack

Press **Ctrl+C** in its terminal, then remove the stopped development container if desired:

```sh
docker compose -f docker-compose.dev.yml down
```

Without `-v`, development data is kept.

## Build a production image locally

From the root:

```sh
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

This uses the production Compose project and ports. Use an isolated checkout/project if you already run production Faro; do not accidentally replace the server your network depends on.

## Keep the guides current

When a workflow changes, update its task guide, verify button labels, and check links from [the guide index](README.md) and [main README](../README.md). For interface screenshots, use a separate browser session with example data and wait for charts and tables to load before capturing. The published images live in `docs/screenshots/`.

After replacing the PNG captures, run `python tools/frame-screenshots.py` from the repository root (Python 3 required). This refreshes the self-contained SVG frames used for rounded corners in the README and guides. Full-size download links still point to the original PNGs.
