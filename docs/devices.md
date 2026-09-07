# Manage devices

[All guides](README.md) · Related: [Protection](protection.md) · [UniFi](unifi.md)

**Devices** lists DNS clients Faro has observed. A device appears after sending requests; this is not a scan of everything connected to your network.

![Device inventory with example data](screenshots/devices.svg)

## Name a device

1. Find the device by name or IP and select it.
2. In **Overview**, find **Edit device**.
3. Enter a **Friendly name**, such as `Living room TV`.
4. Optionally add a location and notes or choose a type and icon. **Automatic** lets Faro identify the type.
5. Choose **Save device** and check the inventory.

Your manual identification takes precedence over automatic suggestions.

## Change its protection

1. Open the device's details.
2. Use **Choose protection** to select Home or another setup.
3. Check the assigned protection shown for the device.
4. Make a fresh lookup from that device and inspect it in **Activity**.

The assignment applies immediately. [Create a protection setup](protection.md) first if needed.

## Review its history

1. Open the device and select **Activity replay**.
2. Choose a time range.
3. Press **Play** or move the cursor to reveal requests over time.
4. Select a domain to inspect it.

Playback is limited to the first 2,500 requests in the period; summaries include all requests in that period. A lookup is not a confirmed website visit.

## Temporarily pause DNS access

1. Open the device's **Overview**.
2. Find **Device internet pause** and choose a duration.
3. Choose **Restore internet access** to end it early.

This affects DNS through Faro. Existing connections, cached answers, and other DNS services may still work; it is not a router firewall rule.

To allow normally blocked domains instead, [pause protection](protection.md#pause-blocking).

## Keep assignments predictable

Use DHCP reservations for important devices. DNS policies depend on the source addresses Faro sees. [UniFi](unifi.md) can help correlate identities across address changes. If everything appears under your router, check [device connections](connect-devices.md#if-requests-are-missing).
