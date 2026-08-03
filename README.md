<p align="center">
  <img src="frontend/public/logos/web/icon-512.png" alt="Faro lighthouse logo" width="128" />
</p>

<h1 align="center">Faro</h1>

<p align="center">
  Self-hosted DNS for people who want to understand their network, not just block ads.
</p>

Faro gives you a clear view of the DNS activity happening across your network.

You can see which devices are making requests, what they are connecting to, what was blocked, what was allowed, and why. Everything stays on your network. No cloud account, external dashboard, or third-party telemetry required.

<p align="center">
  <img src="docs/faro-screenshot-1.png" alt="Faro dashboard showing network health, DNS traffic, devices, and upstream resolvers" width="900" />
</p>

<p align="center">
  <sub>Your network health, DNS activity, and devices in one place.</sub>
</p>

## Why Faro?

Most self-hosted DNS tools are good at blocking domains, but they do not always make it easy to understand what is actually happening on your network.

Faro was built to make DNS easier to inspect and operate.

* See which device generated each request
* Understand why a request was allowed or blocked
* Track device activity over time
* Compare upstream DNS providers and latency
* Manage local DNS records and protection rules
* Keep DNS history and configuration on your own hardware
* Run without a cloud account or external service

Faro uses CoreDNS for DNS resolution and adds the management, visibility, device identity, protection, and operational tooling around it.

## Features

* Guided first-run setup and local administrator authentication
* Live dashboard and searchable DNS activity
* Device inventory, friendly names, and activity replay
* Local, read-only UniFi integration for stable device identity across IP changes
* Home-wide protection and custom per-device protection setups
* Local DNS records, per-protection exceptions, and curated blocklists
* Encrypted DNS-over-HTTPS upstreams with health checks and failover
* Upstream DNS provider selection with live latency comparisons
* DNS cache and upstream-resolution visibility
* Clear explanations for why requests were allowed or blocked
* Secure multi-server redundancy with one primary Faro server and any number of read-only DNS replicas
* Configurable retention, database pruning, and health metrics
* Downloadable, passphrase-encrypted database backups with in-app restore

## Design Goals

Faro is built around a few simple ideas:

* Your DNS data should stay on your network
* The interface should explain what is happening instead of hiding it
* Basic deployment should be simple
* Advanced behavior should still be inspectable
* Device identity should survive IP address changes
* DNS failures should be visible and understandable
* Running Faro should not require an external account

## How It Fits

```text
                    Internet
                       │
              Upstream DNS Providers
             Cloudflare, Quad9, others
                       │
                 ┌───────────┐
                 │   Faro    │
                 │           │
                 │ CoreDNS   │
                 │ Web App   │
                 │ Database  │
                 └───────────┘
                       │
                 Router / DHCP
                       │
        ┌──────────────┼──────────────┐
        │              │              │
      Phones         Computers      Servers
      TVs            Consoles       IoT Devices
```

Your router provides Faro as the DNS server for devices on the network. Faro handles DNS resolution, records activity, applies protection rules, and exposes everything through the web interface.

## Run Faro

You need Docker Compose and a machine with a fixed LAN IP or DHCP reservation.

```sh
mkdir faro && cd faro
curl -LO https://raw.githubusercontent.com/derek-diaz/Faro/main/docker-compose.yml
docker compose up -d
```

On Windows PowerShell:

```powershell
New-Item -ItemType Directory faro -Force | Out-Null
Set-Location faro
Invoke-WebRequest https://raw.githubusercontent.com/derek-diaz/Faro/main/docker-compose.yml -OutFile docker-compose.yml
docker compose up -d
```

Open:

```text
http://YOUR-FARO-IP:1787
```

Create the administrator account and complete the guided setup.

Account creation remains open until the first administrator account is created. Faro then closes account creation automatically.

Once setup is complete, configure your router's DHCP DNS server to use:

```text
YOUR-FARO-IP
```

| Port   | Protocol    | Purpose                         |
| ------ | ----------- | ------------------------------- |
| `1787` | TCP         | Faro web interface              |
| `53`   | TCP and UDP | DNS for your router and devices |

> Port `53` must be available on the Docker host for normal router-wide DNS use.

## Update Faro

Run these commands from the directory containing `docker-compose.yml`:

```sh
docker compose pull
docker compose up -d
```

For port customization, verification, troubleshooting, backups, local development, architecture, and release publishing, see the [technical and deployment guide](docs/README.md).

## Unraid

Unraid runs the same `tabierto/faro` image used by the standard Docker Compose deployment.

The Community Applications template only translates Faro's normal ports, volume, and environment settings into the Unraid interface.

See the [Unraid installation notes](docs/unraid.md).

## License

Copyright 2026 Derek Diaz Correa.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the full license text.

<p align="center">
  Made in Puerto Rico.
</p>
