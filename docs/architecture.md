# Understand how Faro works

[All guides](README.md) · Related: [Development](development.md)

Faro ships as one container with separate processes inside it. This keeps installation simple while allowing DNS to continue through some management-interface failures.

## Follow a lookup

```text
Device → CoreDNS → Local record, blocking decision, or cached answer
            │
            └─ Needs an upstream answer
                 ├─ Standard mode → DNS provider
                 └─ Encrypted mode → Local HTTPS gateway → DNS provider

DNS query output → Bounded raw logs → API ingestion → SQLite → Web interface
```

| Part | Responsibility | Where to look |
| --- | --- | --- |
| React interface | Pages, forms, charts, and activity inspection. | [frontend/src](../frontend/src) |
| Go API | Authentication, settings, database access, and background jobs. | [cmd/faro-api](../cmd/faro-api), [internal/api](../internal/api) |
| CoreDNS management | Generate, validate, apply, and roll back resolver configuration. | [internal/coredns](../internal/coredns) |
| Encrypted gateway | Forward accepted DNS requests over HTTPS. | [cmd/faro-dohproxy](../cmd/faro-dohproxy), [internal/dohproxy](../internal/dohproxy) |
| Query logging | Bound raw log files and ingest retained history. | [cmd/faro-logtee](../cmd/faro-logtee), [internal/querylog](../internal/querylog) |
| SQLite storage | Store settings, identities, retained history, and migration state. | [internal/db](../internal/db) |
| Container supervision | Start processes and handle shutdown or failure. | [docker](../docker) |

## Follow a settings change

1. The API builds a candidate resolver configuration from the saved settings.
2. It validates the staged files with CoreDNS before replacing active files.
3. It checks that the running resolver accepted the new configuration.
4. On failure, it restores the previous accepted state.
5. The encrypted gateway receives the accepted provider snapshot rather than reading unfinished edits.

Inspect the result in **Settings → Advanced**. Make changes through Faro's normal pages; editing generated files directly can be overwritten by later saves.

## Understand failure behavior

- If the interface or API fails, the DNS processes can keep serving the last accepted configuration while health checks report a management failure.
- If CoreDNS or the encrypted gateway exits, the container stops so Docker can restart it.
- If history storage fails, Faro reports the recording problem separately from DNS availability.
- If the primary becomes unreachable, replicas keep their last accepted configuration. They are not automatically promoted.

These boundaries improve availability but do not replace backups or deployment-specific testing.

## Inspect a running installation

1. Check `docker compose ps` and recent logs.
2. Open **Settings → Health & data** for API memory and storage information.
3. Open **Settings → Advanced** for accepted resolver files and configuration health.
4. For monitoring integrations, inspect `/healthz` and `/metrics` through Faro's web port. Metrics include DNS, database, and API runtime information; they can reveal local network details.

Use [troubleshooting](troubleshooting.md) for symptom-based steps and [storage](storage.md) for retention changes.
