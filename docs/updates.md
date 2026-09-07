# Update Faro

[All guides](README.md) · Before this: [Make a backup](backup-restore.md)

Use this guide for Docker Compose. Unraid users should follow [the Unraid update steps](unraid.md#update-and-back-up).

## Install the current release

1. Download an encrypted backup and keep its passphrase.
2. Open a terminal in the folder containing your `docker-compose.yml`.
3. Run:

   ```sh
   docker compose pull
   docker compose up -d
   docker compose ps
   ```

4. Wait for Faro to start, then sign in and check the version shown in the interface.
5. Run `nslookup example.com YOUR-FARO-IP` from another device.
6. Check **Activity** and your local records.

Container replacement briefly interrupts this server. Your `faro-config` volume is kept. Before a database schema change, Faro creates an automatic migration backup too.

## Follow releases or pin a version

By default, Compose uses `latest`. If you have set `FARO_VERSION` in `.env`, updates continue using that tag.

1. Open `.env` beside `docker-compose.yml`.
2. To follow releases, remove the `FARO_VERSION` line or set it to `latest`.
3. To pin a release, set it to that release's published image tag, without the leading `v` used in Git tags.
4. Run the update commands above.

Choose a real published tag from the project's release information. Do not downgrade a database to an older image unless that image supports its schema.

## If Faro fails after an update

1. Keep the current data and all migration backups.
2. Read the logs:

   ```sh
   docker compose logs --tail=200 faro
   ```

3. If the container is running, inspect the upgrade status:

   ```sh
   docker compose exec faro cat /config/faro-upgrade.json
   ```

4. If it cannot stay running, stop it and read the same file with a temporary container:

   ```sh
   docker compose stop faro
   docker compose run --rm --no-deps --entrypoint cat faro /config/faro-upgrade.json
   ```

   The temporary command mounts the same data volume without starting Faro. If you customized the upgrade-state path, use that path instead. A missing file can mean no migration state has been written yet; use the startup logs.

5. Use the result below to choose the next action.

| Result | Next action |
| --- | --- |
| Failed migration with successful automatic recovery | The logs identify the failure and recovery. Preserve a [complete folder copy](backup-restore.md#make-a-complete-folder-copy), then use the last working image only if the recovered schema is compatible. |
| `incompatible` | Run a release that supports the stored schema, or restore a backup made by a compatible release. An older image is not automatically a valid rollback. |
| Automatic restoration also failed | Preserve the complete data folder. Recover from a known-good full-folder backup, or restore an encrypted backup into a fresh installation using the [move procedure](backup-restore.md#move-to-another-machine). Avoid replacing individual live SQLite files. |
| No migration error | Follow [startup troubleshooting](troubleshooting.md#faro-will-not-start). |

After correcting the cause or selecting the compatible image, run `docker compose up -d` and repeat the DNS test. Automatic migration copies are stored under `/config/migrations` by default; they are SQLite copies, not `.faro-backup` files for the web restore form.
