# Connect a local UniFi controller

[All guides](README.md) · Related: [Manage devices](devices.md)

This optional integration imports device names and addresses. Faro reads UniFi data; it does not change DHCP, DNS, firewall, or Wi-Fi settings.

## Before you start

You need a local UniFi Network installation that offers the Network API, its private HTTPS address, and a local API key. A Site Manager cloud key or cloud URL is not the same connection.

## 1. Create an API key in UniFi

1. Open your local UniFi Network application.
2. Find **Integrations**. Depending on the version, it is under **Settings → Control Plane → Integrations**.
3. Create an API key for Faro and copy it for the next step.

UniFi's local API documentation is version-specific. If labels differ, follow [Ubiquiti's official API guide](https://help.ui.com/hc/en-us/articles/30076656117655-Getting-Started-with-the-Official-UniFi-API).

## 2. Connect from Faro

1. Open **Settings → Integrations → UniFi Network**.
2. Enter the console's local HTTPS URL, such as `https://192.168.1.1`.
3. Paste the API key and choose **Test connection**.
4. If a self-signed certificate is reported, compare the displayed fingerprint with the console's certificate. If it matches the console you intend to trust, confirm it and choose **Trust and test again**.
5. Select the UniFi site you want.
6. Choose **Connect and sync**.

Faro encrypts the stored API key and does not display it again. It accepts local addresses, not public cloud endpoints.

## 3. Check the result

1. Check **Devices synchronized** and **Last synchronized**.
2. Open **Devices** and inspect imported names and addresses.
3. Use **Sync now** if you want an immediate refresh. Otherwise synchronization runs about once per minute.

Manual Faro names and device choices take precedence over imported suggestions. Traffic and blocking still come from DNS requests Faro observes.

## If synchronization stops

- Re-test the local URL and API key.
- Check that Faro can reach the console and that the selected site is correct.
- If the console certificate changed, review the new certificate before trusting it; Faro pins an approved self-signed certificate.
- After restoring onto a fresh installation, reconnect UniFi. Portable backups do not include its credentials or imported observations.

To disconnect, choose **Disconnect** in the integration panel and confirm the displayed impact.
