# Faro on Unraid

Unraid runs the same `tabierto/faro` image used by standard Docker Compose. The template only presents Faro's normal ports, `/config` volume, and optional environment settings in the Unraid interface; there is no separate Unraid edition or container architecture.

## Install

1. Add the [`templates/faro.xml`](../templates/faro.xml) template to Unraid or install Faro from Community Applications after it is published.
2. Keep the application-data mapping at `/mnt/user/appdata/faro` or choose another cache-backed appdata directory.
3. Publish TCP and UDP port `53`, and choose the web interface port (default `1787`).
4. Open `http://YOUR-FARO-IP:1787` and complete the guided setup.
5. Configure the router's DHCP DNS server to distribute Faro's fixed address.

TCP and UDP port `53` must be available. Giving Faro its own static LAN IP through an Unraid custom `ipvlan` or `macvlan` network is recommended when the Unraid host already uses port `53`; it also gives the router one clear address to distribute as DNS.

## Persistent data

The single `/config` mapping contains:

- `faro.db`: SQLite configuration and retained activity
- `coredns`: generated and last-known-good resolver configuration
- `favicons`: optional cached domain icons
- `logs`: bounded raw DNS query-log buffers

For routine backups, use **Settings → Health & data → Encrypted backup & restore**. Backing up `/mnt/user/appdata/faro` provides a complete host-level recovery copy.

## Updating

Use **Check for Updates** on Unraid's Docker page. The web interface, API, and DNS engine update atomically with the same Faro image published for every supported Docker installation.
