# Connect devices and your router

[All guides](README.md) · Before this: [Install Faro](installation.md)

Use this guide after Faro answers a direct lookup. Examples use `192.168.1.10` for Faro's fixed LAN address.

## Try one device first

1. Open the device's network settings and find DNS for its current Wi-Fi or wired connection.
2. Change DNS from automatic to manual.
3. Enter Faro's IP address. Remove other resolver addresses for this test.
4. Save and reconnect the device.
5. Browse a few sites and check **Activity** in Faro.

You should see requests from the device. To undo the test, return DNS to automatic and reconnect.

## Connect the whole network

1. Sign in to your router's local administration page.
2. Find its **LAN**, **DHCP**, or **DNS server** settings. Labels vary by router.
3. Set the DNS server distributed to clients to Faro's fixed IP address.
4. Save the setting.
5. Reconnect devices or renew their DHCP leases so they receive the new address.
6. Check **Devices** and **Activity** in Faro.

Changing WAN DNS may only change where the router sends its own lookups. Use LAN/DHCP settings when you want devices to contact Faro directly.

## If your router asks for two DNS servers

For one Faro installation, leave the second field empty if permitted. A public resolver there can bypass Faro's filtering; devices do not always treat it as an emergency-only backup.

For a second address with the same protection, [pair another Faro server](redundancy.md) and advertise both Faro addresses.

## If requests are missing

- **Only the router appears:** it may be forwarding all requests under its own IP. Configure DHCP to advertise Faro directly when supported.
- **Some devices are missing:** check manual DNS, VPNs, browser secure DNS, and operating-system private DNS. They may send lookups elsewhere.
- **IPv6 is enabled:** review DNS addresses advertised over IPv6 too. An unrelated IPv6 resolver can bypass Faro even when IPv4 is configured correctly.
- **The interface works but DNS times out:** allow port 53 over both TCP and UDP from your LAN in the host firewall. See [troubleshooting](troubleshooting.md).

**You are done when:** requests appear from the devices you expect and those devices can still open websites.

Next: [install blocklists](blocklists.md) and [choose protection](protection.md).
