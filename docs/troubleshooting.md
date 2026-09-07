# Troubleshoot Faro

[All guides](README.md)

Pick the symptom below. If only one site or app is broken, start with [Fix a broken site](fix-a-site.md).

## Faro will not start

1. Open a terminal in the folder containing `docker-compose.yml`.
2. Check status and recent logs:

   ```sh
   docker compose ps
   docker compose logs --tail=200 faro
   ```

3. Look for the first concrete error: a port conflict, missing permission, full disk, or database upgrade failure.
4. Follow the matching section below. For migration errors, use [Update Faro](updates.md#if-faro-fails-after-an-update).
5. After fixing the cause, run `docker compose up -d` and repeat a DNS lookup.

## Port 53 is already in use

Identify the service before changing it.

Linux:

```sh
sudo ss -lntup '( sport = :53 )'
```

macOS:

```sh
sudo lsof -nP -iTCP:53 -sTCP:LISTEN
sudo lsof -nP -iUDP:53
```

Windows PowerShell:

```powershell
Get-NetTCPConnection -LocalPort 53 -ErrorAction SilentlyContinue
Get-NetUDPEndpoint -LocalPort 53 -ErrorAction SilentlyContinue
```

On Windows, use the reported owning-process ID with `Get-Process -Id PROCESS-ID` to identify it.

If the existing DNS service only listens on a loopback address such as `127.0.0.53`, try binding Faro to the host's fixed LAN address in `.env`:

```dotenv
FARO_BIND_ADDRESS=192.168.1.10
FARO_DNS_PORT=53
```

Replace the example address and run `docker compose up -d`. If the other service occupies that LAN address or all addresses, reconfigure it deliberately or use a different host/IP for Faro. Do not disable a DNS service without identifying what depends on it.

## The interface opens but DNS fails

1. Test Faro directly: `nslookup example.com YOUR-FARO-IP`.
2. Check both published protocols:

   ```sh
   docker compose port --protocol udp faro 53
   docker compose port --protocol tcp faro 53
   ```

3. Both should show the intended host address and port 53. Check `docker compose config` if they do not.
4. Allow TCP and UDP port 53 from your LAN in the host firewall.
5. Open **Upstreams**, refresh provider health, and check that at least one selected provider works.
6. Repeat the lookup.

If you customized a Compose file, compare its port mappings with the repository's [standard file](../docker-compose.yml) before changing it. Preserve your custom settings.

## The interface does not open

1. Confirm the host IP and configured web port.
2. Run `docker compose ps` and check the published port.
3. Try `http://YOUR-FARO-IP:1787/healthz`, adjusting the port if customized.
4. Check host firewall access to that port from your LAN.
5. Read the container logs if health is failing or the container is restarting.

A working DNS lookup does not prove the interface is healthy; DNS can continue while the API or web process has a problem.

## Activity is empty or only shows the router

1. Send a direct lookup to Faro and check **Activity** with a recent time range and no search filter.
2. If that works, review the router's LAN/DHCP DNS setting and reconnect clients.
3. Check VPNs, browser secure DNS, private DNS, and advertised IPv6 DNS addresses.
4. If every request has the router's IP, configure DHCP to advertise Faro directly when supported.
5. If redundancy is enabled, remember that the primary does not merge replica activity.

See [Connect devices](connect-devices.md) for the complete setup.

## Blocking does not match my expectations

1. Find a fresh request in **Activity** and open its domain.
2. Check which device and protection are involved.
3. Confirm the blocklist is enabled and selected in that protection.
4. Check exceptions, schedules, and temporary pauses.
5. Check upstream filtering if Faro allowed the lookup but the answer is still blocked.
6. Retry after cached answers expire.

DNS cannot remove every ad, and traffic sent to another resolver bypasses Faro.

## Disk space is full or activity storage is paused

1. Read the reason under **Settings → Health & data**.
2. Inspect Docker storage:

   ```sh
   docker system df -v
   ```

3. Check free host space and, on Docker Desktop, the virtual disk's allocation.
4. Expand available storage or remove data you have identified as disposable. Do not delete Faro's volume to clear space.
5. Refresh health and make a new lookup. Faro retries history writes automatically.
6. Set a suitable [history retention period](storage.md) once writes work again.

A database file may need compaction to shrink after pruning. Docker logs and raw DNS logs have separate [limits](configuration.md#log-and-data-options).

## Collect useful details for a bug report

1. Note the Faro version, installation method, symptom, and time it happened.
2. Include the result of a direct DNS lookup and relevant recent log lines.
3. If the interface works, inspect **Settings → Advanced** for configuration errors.
4. Remove API keys, passwords, pairing codes, and private names or addresses you do not want to share.

Report what you expected, what happened, and the smallest steps that reproduce it. This is more useful than an entire history database.
