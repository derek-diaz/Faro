# Install Faro with Docker Compose

[All guides](README.md) · Next: [Connect devices](connect-devices.md)

This guide gets Faro running on one machine. For Unraid, use the [Unraid guide](unraid.md).

## Before you start

- Install Docker with Docker Compose on a machine that stays on.
- Give that machine a fixed local IP address or reserve its address in your router. This guide uses `192.168.1.10` as an example.
- Make sure ports **53 TCP and UDP** and **1787 TCP** are available. [Resolve a port conflict](troubleshooting.md#port-53-is-already-in-use) if needed.

Check that Docker is ready:

```sh
docker version
docker compose version
```

Both should return version information. If Docker cannot connect to its engine, start Docker before continuing.

## 1. Download the Compose file

Linux or macOS:

```sh
mkdir faro && cd faro
curl -fLO https://raw.githubusercontent.com/derek-diaz/Faro/main/docker-compose.yml
```

Windows PowerShell:

```powershell
New-Item -ItemType Directory faro -Force | Out-Null
Set-Location faro
Invoke-WebRequest https://raw.githubusercontent.com/derek-diaz/Faro/main/docker-compose.yml -OutFile docker-compose.yml
```

Keep this folder. You will use it to update and manage Faro.

## 2. Start Faro

Run from that folder:

```sh
docker compose up -d
docker compose ps
```

Wait for startup to finish. If the container keeps restarting, [check its logs](troubleshooting.md#faro-will-not-start).

## 3. Complete setup

1. Open `http://192.168.1.10:1787`, using your machine's address.
2. Create your administrator username and password in the setup screen. No terminal command is needed to create the account. Account creation closes after the first administrator is created.
3. Enter Faro's fixed LAN address. The recommended setup uses **Cloudflare Standard over HTTPS**, **OISD small**, a five-minute DNS cache, and `.home` local names. Cloudflare Standard adds no provider filtering; Faro applies the starter blocklist.
4. Choose **Customize settings** if you want different providers, protection, local names, or cache behavior. Encrypted upstreams cover Faro's public lookups; devices still use ordinary DNS to reach Faro.
5. Choose **Apply and connect**. Faro applies the configuration before showing connection instructions.
6. Point one device's DNS at Faro, open a few websites, then choose **Check for DNS activity**. Seeing a request confirms that request reached Faro, not that all devices use it or that a website loaded.
7. Choose **Finish setup and open Faro** after requests appear. If you are not ready to change DNS, choose **Finish setup and connect later**; this finishes configuration without claiming the connection was checked.

## 4. Check DNS

From another machine on the same network, run:

```sh
nslookup example.com 192.168.1.10
```

You should receive an address for `example.com`. Open **Activity** in Faro and check for the request.

**You are done when:** the web interface opens and a DNS lookup sent directly to Faro succeeds. Next, [connect your devices](connect-devices.md). Installation alone does not change their DNS settings.

Docker keeps Faro's settings and history in the `faro-config` volume mounted at `/config`. Normal container replacement keeps this data. Use [encrypted backups](backup-restore.md) for a portable copy.
