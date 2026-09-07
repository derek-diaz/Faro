# Faro guides

[Back to Faro](../README.md)

Choose what you want to do. Each guide explains where to go, what to change, and how to check the result.

## Start here

1. Install Faro with [Docker Compose](installation.md) or [Unraid](unraid.md).
2. [Connect your devices and router](connect-devices.md).
3. [Install blocklists](blocklists.md) and [choose protection](protection.md).

## Use Faro

| I want to… | Guide |
| --- | --- |
| Understand requests and blocked domains | [Explore activity](activity.md) |
| Name devices, review history, or pause DNS access | [Manage devices](devices.md) |
| Block domains, allow exceptions, or set a schedule | [Manage protection](protection.md) |
| Install, update, or remove filtering lists | [Manage blocklists](blocklists.md) |
| Give a local server an easy name | [Add local DNS records](local-dns.md) |
| Change DNS providers or enable encrypted upstream DNS | [Choose upstream providers](upstreams.md) |
| Change caching, appearance, or my password | [Change settings](settings.md) |
| Find out why a site stopped working | [Fix a broken site](fix-a-site.md) |

## Look after your installation

| I want to… | Guide |
| --- | --- |
| Save a backup, restore it, or move to another machine | [Back up and restore](backup-restore.md) |
| Install a newer release or recover from an upgrade problem | [Update Faro](updates.md) |
| Keep less history or free database space | [Manage storage](storage.md) |
| Change Docker ports or deployment settings | [Configure Docker](configuration.md) |
| Import device names from UniFi | [Connect UniFi](unifi.md) |
| Add or remove a second Faro DNS server | [Set up redundancy](redundancy.md) |
| Fix installation, DNS, or storage problems | [Troubleshoot Faro](troubleshooting.md) |

## Work on Faro

These guides are for contributors. You do not need them to run Faro.

- [Run and test the source code](development.md)
- [Understand the architecture](architecture.md)
- [Add device recognition rules](device-catalog.md)
- [Publish a release](releasing.md)

## Useful terms

| Term | Meaning in Faro |
| --- | --- |
| DNS | The service that looks up addresses for names such as `example.com`. |
| Upstream provider | The DNS service Faro asks when it cannot answer locally. |
| Blocklist | A list of domains to block. |
| Protection | A saved combination of blocklists, exceptions, and a blocking schedule. |
| Home | The default protection for devices without another assignment. |
| Cache | Recently used answers Faro keeps to answer repeated requests faster. |
| Replica | Another Faro server that answers DNS using configuration from the primary. |

Replace example IP addresses with addresses from your network. Screenshots show example data.
