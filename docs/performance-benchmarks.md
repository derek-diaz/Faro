# Faro performance benchmarks

November 27, 2026 · Faro 0.12.0 working tree, before and after the nine audit fixes.

## Browser experience

The phone profile used Edge at 375 × 812 with touch, a 4 Mbps downlink, 80 ms latency and 4× CPU throttling. Desktop used 1440 × 900 on loopback. Both production builds ran behind their packaged Nginx configuration and received identical fast API fixtures. Each value is the median of three cold browser contexts per version after a warm-up, alternating versions. Dashboard timings start at navigation and end when the totals or chart first appear. Device timings start at the tap and end when populated inventory is visible.

| Measurement | Phone before | Phone after | Desktop before | Desktop after |
| --- | ---: | ---: | ---: | ---: |
| Dashboard totals (ms) | 3,423 | 1,485 | 421 | 422 |
| Traffic chart (ms) | 3,800 | 1,760 | 718 | 719 |
| First visit to Devices (ms) | 179 | 175 | 21 | 20 |
| Return visit to Devices (ms) | 151 | 151 | 15 | 15 |
| JavaScript and CSS through chart display (bytes) | 1,420,568 | 307,983 | 1,420,568 | 307,983 |
| JavaScript and CSS resources (files) | 13 | 8 | 13 | 8 |

## SQLite history workloads

The month fixture contains 600,000 synthetic requests from 25 devices across 5,000 names. Each before/after pair used the same database file. One file had no planner statistics and the other had been analyzed. Each cell is the median of three wall-clock measurements in milliseconds on Windows with an Intel Core i9-14900K. The calendar-day window contained approximately 2,700 requests. Activity summary and dashboard measurements used a cold handler cache.

| Workload (ms) | Fresh before | Fresh after | Analyzed before | Analyzed after |
| --- | ---: | ---: | ---: | ---: |
| Uncached Overview | 129.6 | 56.5 | 79.1 | 35.1 |
| Device inventory, 25 devices | 77.6 | 1.5 | 1.7 | 1.6 |
| Global search, broad substring | 77.5 | 41.5 | 73.6 | 38.3 |
| Global search, one matching domain | 50.9 | 4.1 | 49.0 | 4.7 |
| Global search, no matches | 46.0 | 3.1 | 44.4 | 3.6 |
| Activity, first 50 rows, 30 days | 20.5 | 0.5 | 20.9 | 0.5 |
| Activity, first page, 30-day search miss | 280.8 | 186.5 | 275.2 | 185.9 |
| Activity, 24-hour summary | 46.0 | 5.7 | 46.0 | 6.1 |
| Activity, 30-day summary | 261.5 | 223.6 | 323.9 | 227.9 |
| Activity, 30-day summary search miss | 444.2 | 141.3 | 508.0 | 143.2 |
| Notification candidates | 20.2 | 0.5 | 20.3 | 0.5 |
| Hourly chart query | 6.8 | 6.7 | 6.7 | 6.9 |
| Home rule preview | 25.7 | 24.8 | 25.3 | 25.1 |

## Household benchmark and history writes

The repository household fixture concentrates 200,000 requests into 100 domain names across 10 devices. The existing Go benchmark ran three iterations per operation. For the earlier implementation the summary time is the sum of its count and timeline queries.

| Household read (ms/operation) | Before | After |
| --- | ---: | ---: |
| Activity first 50 rows | 8.8 | 0.5 |
| Activity counts and timeline | 70.5 | 54.6 |
| Domain drawer | 2.5 | 1.7 |
| Device details | 21.3 | 21.9 |
| Uncached Overview | 85.8 | 83.3 |

Run the household read benchmark from the repository root.

```sh
go test ./internal/api/handlers -run '^$' -bench '^BenchmarkHistoryReads$' -benchtime=3x -count=1 -benchmem
```

The history writer used 16 client addresses and batches of 256. Each measurement ran 20 batches. Values below are medians of three repeated runs per version, in rows per second.

| History writer mix (rows/s) | Before | After |
| --- | ---: | ---: |
| Repeated local name | 26,234 | 25,456 |
| Forwarded names | 28,495 | 26,700 |
| Locally handled names | 6,992 | 6,269 |
| Half forwarded and half locally handled | 10,912 | 10,680 |

## What changed in use

- A cold Overview makes six API requests, down from nine. Records, blocklists and protection collections load when a page needs them. The packaged image precompresses JavaScript and CSS, serves fingerprinted assets with immutable caching, and revalidates the HTML for new releases.
- A device tap or direct device URL now makes one detail request. Previously a tap made two and a reload made three. A changed inventory revision triggers one refresh, and an unchanged revision leaves the open drawer alone. Unsaved alias edits survive the refresh.
- The Activity summary gets counts and timeline buckets from one SQLite snapshot. Across 35 simulated seconds a closed custom hour issued zero automatic requests at both screen sizes. Manual refresh fetched rows and summary once each, and a live range kept polling.
- The device list seeks today's requests for each recorded device ID. Activity and notifications skip repeated addresses while preserving older clients missing from the directory.
- Dashboard rankings use the time index when today is a small share of retained history. In a 2,440-row daily window, ranking all domains on the fresh month-long fixture fell from 46.29 to 0.53 ms. A separate 200,000-request full-day check put the entire uncached dashboard at 164.5 ms before and 166.0 ms after across nine iterations.
- Global search visits distinct domain names, ranks matches by recency and counts the eight winners. A browser check confirmed that replacing a search or closing the dialog disconnects the pending HTTP request.
- Refreshing an unchanged 100,000-domain list took 323 ms before and 60 ms after, using the median of three refreshes. Additional DNS applies fell from one per refresh to zero. A changed list still applies, and a failed apply restores the previous list. This fixture used a local source and counted calls to the apply callback.

## Flow checks

Browser tests used 375 × 812 touch and 1440 × 900 desktop viewports. They covered creating the first admin in the UI, completing DNS setup, connecting the first device, following a dashboard hour into Activity, refreshing historical results, opening device troubleshooting, and searching for a domain. The end-to-end checks used the Go HTTP API, a fresh SQLite database and an injected DNS observation. Additional checks covered drawer Back navigation, retained form edits, device revalidation, creating a local record, blocklist and protection, and recovering from an API failure.

The full Go test suite, `go vet ./...` and the TypeScript/Vite production build passed. The frontend unit suite passed 12 tests and skipped the opt-in check against the live Packettale catalog. Packaged Nginx checks verified byte-for-byte gzip decoding, identity encoding, cache headers, SPA deep links and missing assets.

Timings are local workstation measurements. The mobile figures use browser emulation. The history writer measures persisted rows per second. Resolver round trips, WAN jitter and physical phone performance require separate measurements.
