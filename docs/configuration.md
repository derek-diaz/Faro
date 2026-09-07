# Configure Docker ports and data options

[All guides](README.md) · Related: [Install Faro](installation.md) · [Settings in the app](settings.md)

These settings belong to the Docker deployment. Change protection, DNS providers, and retention in Faro's interface instead.

## Change the web port or bind address

1. Open the folder containing `docker-compose.yml`.
2. Create or edit a file named `.env` in that folder. On Windows, check that it is not saved as `.env.txt`.
3. Add only the overrides you need. For example:

   ```dotenv
   FARO_BIND_ADDRESS=192.168.1.10
   FARO_UI_PORT=8088
   FARO_DNS_PORT=53
   ```

4. Replace the example IP with the host's actual fixed LAN address.
5. Check and apply the configuration:

   ```sh
   docker compose config
   docker compose up -d
   ```

6. Open `http://192.168.1.10:8088` and test DNS on the same IP.

Binding to a LAN address publishes both the interface and DNS there. Keep port 53 for router-wide DNS; most routers cannot advertise a custom DNS port.

## Port and image options

| Variable | Default | Purpose |
| --- | --- | --- |
| `FARO_BIND_ADDRESS` | `0.0.0.0` | Host address for published ports. |
| `FARO_UI_PORT` | `1787` | Host web-interface port. |
| `FARO_DNS_PORT` | `53` | Production host DNS port, TCP and UDP. |
| `FARO_DEV_DNS_PORT` | `5354` | DNS port for the development Compose file only. |
| `FARO_IMAGE_NAMESPACE` | `tabierto` | Namespace containing the Faro Docker image. |
| `FARO_VERSION` | `latest` | Published image tag. See [updates](updates.md). |

The API is internal to the container. The web server forwards API requests; you do not need to publish a separate API port.

## Log and data options

| Variable | Default | Purpose |
| --- | --- | --- |
| `FARO_QUERY_LOG_MAX_BYTES` | `10485760` | Maximum bytes in one raw DNS log file. |
| `FARO_QUERY_LOG_BACKUPS` | `2` | Rotated raw DNS log files to retain. |
| `FARO_DOCKER_LOG_MAX_SIZE` | `10m` | Maximum size of one Docker output log. |
| `FARO_DOCKER_LOG_BACKUPS` | `3` | Docker output log files to retain. |
| `FARO_DEVICE_CATALOG_PATH` | `/config/device-catalog.json` | Optional custom [device recognition catalog](device-catalog.md). |
| `FARO_MIGRATION_BACKUP_DIR` | `/config/migrations` | Automatic database migration backups. |
| `FARO_UPGRADE_STATE_PATH` | `/config/faro-upgrade.json` | Database upgrade status file. |

After editing `.env`, run `docker compose up -d`. Docker log-driver changes require container recreation; use `docker compose up -d --force-recreate faro` if changing those limits on an existing container.

Keep custom data paths inside the persistent `/config` mount unless you also configure another persistent mount. The development image uses different data paths; see [development](development.md).

## Stop and start without deleting data

Run from your Faro folder:

```sh
docker compose stop faro
docker compose start faro
```

To remove the container while keeping the volume, use `docker compose down`; recreate it later with `docker compose up -d`.

Do not add `-v` to `docker compose down` unless you intend to permanently delete Faro's volume. Keep the same Compose project/folder when reusing an installation so it finds the same data volume.
