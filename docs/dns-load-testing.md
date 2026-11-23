# DNS load testing

This fixture runs the production image with its real CoreDNS resolver, log rotation,
SQLite history, classifier, API and nginx. It creates a separate database and an
internal Docker network with no published ports. Its upstream serves synthetic
answers with 15–35 ms UDP latency and 25 ms TCP latency; it cannot send the load to
public resolvers.

Build and run from the repository root (Docker Compose and Python's container image
are required; Python need not be installed on the host):

```sh
docker build -f docker/Dockerfile.all-in-one -t faro-load-test:local .
docker compose -p faro-dns-load -f tools/dns-load.compose.yml run --rm load
docker compose -p faro-dns-load -f tools/dns-load.compose.yml down -v
```

Use a unique project name if that project already exists. The fixture reserves
`172.30.254.0/24`; do not use it alongside another network with that subnet. `down -v`
removes only the selected test project's containers, network and database.

Faro is limited to two CPUs and 512 MiB. The generator uses 50 distinct loopback
client addresses inside Faro's network namespace. It sends A and AAAA questions,
approximately 5% over TCP, with this repeating mix:

- 50% popular names eligible for caching (200 names, 60-second upstream TTL).
- 20% unique uncached names.
- 20% blocked names from a 100,000-domain list.
- 5% local DNS records.
- 5% unique nonexistent names.

The four default phases each last 60 seconds: 10, 100, 500 and 2,000 queries/second.
The last two are stress levels, not a claim about ordinary household demand.
During each phase the generator also polls Dashboard, Activity rows and summary,
and Devices. It validates transaction IDs, questions, response codes, answer types
and addresses; waits for history ingestion; checks all 50 clients and duplicate
unique names; and exits unsuccessfully for errors, missing/extra history or a
generator that falls more than 50 ms behind schedule. Reported DNS latency includes
that scheduling delay. Expected NXDOMAIN and blocked responses count as successful
answers.

Environment variables allow focused reruns:

| Variable | Default | Purpose |
| --- | --- | --- |
| `FARO_LOAD_PHASES` | `10:60,100:60,500:60,2000:60` | `queries-per-second:seconds` pairs |
| `FARO_LOAD_LOG_BYTES` | `10485760` | Raw log size before rotation; two backups are retained |
| `FARO_LOAD_RELOAD` | `0` | Set to `1` to change the cache TTL through the API halfway through each phase |
| `FARO_LOAD_PROFILES` | `0` | Set to `1` to assign 40 clients to two additional protection groups |

For a frequent-rotation test, set `FARO_LOAD_LOG_BYTES=1048576`,
`FARO_LOAD_RELOAD=1`, `FARO_LOAD_PROFILES=1`, and `FARO_LOAD_PHASES=500:60,2000:60`.
Run `docker compose ... up -d upstream faro` before `run --rm load` after changing
the log limit, so Compose recreates Faro with the new environment.

This is a repeatable integration load test, not a WAN benchmark. It does not cover
real router/NIC limits, packet loss, encrypted upstream providers or ARM hardware.

## Measured run: November 22, 2026

Tested on Docker Desktop's Linux/amd64 engine (WSL2), using CoreDNS 1.11.3 and the
working production build. Each row below represents 60 seconds of generated DNS
traffic. Expected blocks and NXDOMAIN answers are included in successful requests.

Initial run, one protection profile and the default 10-MiB raw log limit:

| Queries/sec | Queries | DNS p50 | DNS p99 | API p95 | History catch-up after traffic |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 10 | 600 | 20.46 ms | 37.17 ms | 4.28 ms | 2.00 s |
| 100 | 6,000 | 1.82 ms | 36.77 ms | 6.69 ms | 2.02 s |
| 500 | 30,000 | 0.97 ms | 36.22 ms | 25.55 ms | 2.04 s |
| 2,000 | 120,000 | 0.84 ms | 36.17 ms | 186.27 ms | 3.17 s |

All 156,600 requests received correct DNS answers and appeared in history. The
low-rate phase has more uncached answers because the popular-name cycle is longer
relative to the upstream TTL.

The runs exposed three defects that were fixed and covered by regression tests:

- Local AAAA answers could be described using a same-name A record. History now
  selects the local record by question type and includes that type in the batch
  decision cache key. Domain-overview behavior is preserved.
- A settings save during 2,000 queries/second failed with `database is locked`.
  Writable transactions now reserve SQLite's writer before reading. This prevents
  a competing history write from invalidating a transaction's read snapshot, while
  WAL readers remain concurrent. See [SQLite's transaction isolation explanation](https://www.sqlite.org/isolation.html).
- With several hundred thousand history rows, summary queries repeatedly fetched
  large query records to read small counters. During the deliberately constrained
  1-MiB rotation test, API p95 reached 3.25 seconds and ingestion lost 19,212 entries
  after falling beyond the two retained backups. Schema migration 15 adds covering
  indexes for device and dashboard summaries and extends the Activity index with
  its counter fields. The migration uses Faro's existing automatic backup and
  recovery flow. It adds index storage and one-time migration work on existing
  history; no application data is deleted.

The Docker build also unnecessarily copied local scratch files. `.dockerignore`
now excludes scratch directories and local environment files; this does not change
the runtime image's application content.

Final rerun after all fixes, using three profiles, a 1-MiB raw log limit with two
backups, a real configuration reload halfway through each phase, and 438,388
existing history rows at the start:

| Queries/sec | Queries | DNS p50 | DNS p99 | API p95 | History catch-up after traffic |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 500 | 30,000 | 1.10 ms | 36.58 ms | 474.85 ms | 2.14 s |
| 2,000 | 120,000 | 0.85 ms | 36.89 ms | 1,187.21 ms | 5.82 s |

Both phases passed: zero DNS or API errors, all 150,000 queries recorded across
50 clients, zero duplicate unique-name queries, and zero local-record type
mismatches. Reloads took 3.88 and 2.73 seconds. No OOM events or skipped-log warnings
occurred in the final run. The generator stayed within its scheduling tolerance.

The 512-MiB cap remained tight, and API reads were still slower at the highest
stress level. These results establish the tested workload, not unlimited capacity
or guaranteed latency. To repeat the populated-database scenario, keep the same
test project's volume across runs, then remove it with `down -v` when finished.
The much smaller raw log limit deliberately gives ingestion less buffering than
the production default.

Validation also included `go test ./...`, `go vet ./...`, the production Docker
build, the harness's packet-validation self-check, and a real schema 14→15 upgrade
of the populated test database with its backup preserved.

## Dashboard and Activity cleanup comparison

Both production builds started with an empty test database. Each ran for 60 seconds
at each rate with 50 clients, three protection profiles, 100,000 blocked domains,
1-MiB log rotation and a live cache-TTL change during each phase.

| Queries/sec | Queries per build | DNS p99 before → after | API p95 before → after |
| ---: | ---: | ---: | ---: |
| 500 | 30,000 | 36.29 → 36.18 ms | 7.85 → 9.51 ms |
| 2,000 | 120,000 | 36.53 → 36.37 ms | 157.12 → 166.42 ms |

Both builds answered and recorded all 150,000 queries. On the 200,000-row handler
benchmark, two five-iteration trials took 65.8–70.9 ms per uncached dashboard
request before cleanup. Trials on the final code took 62.9–64.1 ms. The mixed
API timings above include dashboard, activity and device inventory calls and
were slightly higher in this pair of runs.

The updated harness measures history catch-up from the last DNS completion while
any final API read finishes. Earlier runs started that timer after API polling
finished, so those catch-up durations are not directly comparable.

### Prolonged traffic on a populated database

The final production image used two CPUs, 50 clients, three protection profiles
and a live settings change in each phase. The database grew across runs. DNS
latency includes client scheduling delays.

| Queries/sec | Duration | Memory cap | Raw log size | Correct replies | History rows | DNS p99 | Dashboard p95 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 100 | 30 s | 512 MiB | 1 MiB | 3,000 / 3,000 | 3,000 | 37.05 ms | 1.66 s |
| 500 | 60 s | 512 MiB | 1 MiB | 30,000 / 30,000 | 30,000 | 36.52 ms | 2.08 s |
| 2,000 | 145 s | 512 MiB | 10 MiB | 289,975 / 290,000 | 289,975 | 36.58 ms | 5.07 s |
| 2,000 | 145 s | 1 GiB | 1 MiB | 290,000 / 290,000 | 290,000 | 36.96 ms | 1.90 s |

The 512-MiB peak run started with 1,567,495 history rows. Linux counted 25 UDP
receive-buffer drops, matching the client timeouts. History recorded every
answered query. The two lower-rate runs started with over 1.85 million rows.
Both met the pacing check and caught up within 3.22 seconds.

The 1-GiB run began with 1,890,470 rows. It passed the pacing, DNS, history
and API checks. History caught up 2.47 seconds after traffic ended. The
settings reload took 1.50 seconds and Activity rows had a p95 of 94.68 ms.

#### What changed during testing

- **History recovery.** With 1-MiB logs and 512 MiB of memory, an early run
  answered 290,000 queries but recorded 222,957. The reader skipped retained
  backups when its checkpoint expired. It now pins the retained files before
  reading them, resumes at the oldest available entry and commits device
  observations, queries and the checkpoint in one transaction per batch. A
  repeat recorded 270,833 rows. The small log files could still expire before
  SQLite caught up.
- **Settings reload.** With 10-MiB logs, the next 290,000-query run had three
  client timeouts and three UDP receive-buffer drops. Reloads now stream
  blocklist rows, reuse hosts-file bytes during validation and leave unchanged
  files in place. The following run recorded all 290,000 queries, although the
  generator dispatched 11 more than 50 ms late.
- **Container CPU quota.** A subsequent one-minute run lost 137 of 120,000
  requests at the port-53 UDP receive buffer. The container saw 32 CPUs despite
  its two-CPU quota. Setting `GOMAXPROCS=2` reduced CoreDNS's observed thread
  count from 37 to 8. The repeat passed all 120,000 queries with 36.56 ms DNS
  p99. Faro's container entrypoints now derive Go parallelism from the CPU
  quota and affinity. An explicit `GOMAXPROCS` takes precedence. Container
  checks covered two CPUs, half a CPU, an unlimited quota and an override.
