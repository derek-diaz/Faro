# Manage history and storage

[All guides](README.md) · Related: [Backups](backup-restore.md) · [Troubleshooting](troubleshooting.md)

Open **Settings → Health & data** to see application health, retained records, and database storage.

## Keep less history automatically

1. Find **Automatic retention**.
2. Set **Keep logs for** to the number of days you need, from 1 to 3,650.
3. Choose **Save retention**.
4. Check the saved retention value.

Faro removes expired DNS queries and system events on startup and every six hours. Shortening retention permanently removes older history when cleanup runs. [Make a backup](backup-restore.md) first if you need to keep it.

## Remove old history now

1. Find **Prune database now**.
2. Set **Delete logs older than** to the age cutoff you want.
3. Review the compaction option if you also want to reclaim unused database file space.
4. Choose **Prune now** and wait for completion.
5. Review the reported deleted records and reclaimed space.

For example, a cutoff of 7 days deletes records older than seven days. This one-time action does not change automatic retention. Compaction may take longer on large databases.

## Understand the storage numbers

| Number | What it tells you |
| --- | --- |
| Database storage | Space currently allocated to the SQLite database file. |
| Reclaimable space | Allocated space no longer holding active data; compaction can reclaim it. |
| Activity records | Retained DNS requests and system events. |
| Process memory | The API's reported Go memory allocation, not total memory for every process in the container. |

Deleting rows does not necessarily shrink the file immediately. SQLite can reuse freed pages for later writes.

## If activity storage is paused

1. Read the reason under **Application health**.
2. If disk space is insufficient, check host storage and Docker's storage allocation.
3. Free space using your normal storage tools, or expand Docker's disk allowance.
4. Refresh health and make a new lookup to check that recording resumes.

Faro retries failed history writes automatically. A history-storage failure can leave DNS working while new activity is missing. Requests that could not be retained may not be recoverable.

Raw DNS logs and Docker logs are separate from retained database history and have their own size limits. See [Docker configuration](configuration.md#log-and-data-options).
