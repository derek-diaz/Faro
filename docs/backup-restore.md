# Back up, restore, or move Faro

[All guides](README.md) · Related: [Updates](updates.md) · [Storage](storage.md)

Use an encrypted Faro backup for routine backups and moving to another machine. It contains configuration, administrator account data, protection, local records, blocklists, and retained history.

## Download a backup

1. Open **Settings → Health & data**.
2. Find **Encrypted backup & restore**.
3. Enter a backup passphrase of at least twelve characters and confirm it.
4. Choose **Download backup** and wait for the `.faro-backup` download.
5. Store the file somewhere other than the Faro host. Keep the passphrase separately.

Faro cannot recover the passphrase. It is not automatically your administrator password.

**Check:** the downloaded file exists and has a nonzero size. For a stronger check, restore it into a separate test installation that your router is not using.

## Restore a backup

Restoring replaces the backup-covered state on this installation, including its account and retained history. Download a backup of the current state first if you may need to return to it.

1. Sign in and open **Settings → Health & data → Encrypted backup & restore**.
2. Select the `.faro-backup` file in the restore section.
3. Enter the passphrase used when that file was created.
4. Read and confirm the replacement notice.
5. Choose **Restore backup** and wait for the result.
6. After success, sign in using the administrator credentials contained in the backup. All active sessions are signed out.
7. Check your local records, protection, and a DNS lookup.

Faro validates the restored DNS configuration. If the running resolver rejects it, Faro reports the failure and rolls back to the previous database and DNS configuration.

## Move to another machine

1. Download a backup from the current server.
2. Install Faro on the new machine with a fixed, unused LAN IP.
3. Create a temporary administrator and finish setup so you can open Settings. Leave your router pointing to the old server for now.
4. Restore the backup on the new machine.
5. Sign in with the restored credentials. Update **Settings → DNS & interface → Faro LAN address** to the new address and save.
6. Reconnect UniFi and pair replicas if needed. Review local records that refer to changed addresses.
7. Test directly with `nslookup example.com NEW-FARO-IP`.
8. Change your router's advertised DNS address, reconnect devices, and check new activity before retiring the old server.

## What a portable backup leaves out

| Excluded item | What to do after restoring |
| --- | --- |
| Active login sessions | Sign in again. |
| UniFi credentials and imported observations | Reconnect UniFi on a fresh installation. |
| Replica membership and pairing secrets | Pair servers again on a fresh installation. |
| Temporary troubleshooting exceptions | Start new tests if needed. |
| Cached icons and raw query-log buffers | Faro recreates these as needed. Retained database history is included. |

Excluded integration and replica state already on an existing installation remains local to it; the backup does not import another server's relationships.

## Make a complete folder copy

For host recovery, you can also copy all of `/config`, including local secrets and generated files. This copy is not an encrypted portable backup.

For Docker Compose, run from your Faro folder, using a new destination folder:

```sh
docker compose stop faro
docker compose cp faro:/config ./faro-config-backup
docker compose start faro
```

Check that the copy succeeded and contains `faro.db`. If copying fails, start Faro again before investigating. DNS is unavailable from this server while it is stopped.

On Unraid, stop Faro, copy its appdata folder using your normal backup tool, and start it again. Keep the complete folder together. Do not copy only `faro.db` while Faro is running; SQLite may also have live WAL and shared-memory files.
