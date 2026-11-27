# Install and manage blocklists

[All guides](README.md) · Next: [Apply protection](protection.md)

Open **Protection → Blocklists** to manage filtering sources. When creating a protection setup, Standard and Stricter filtering can install their source within the setup flow.

## Install a listed source

1. Open **Protection → Blocklists → Available**.
2. Search or filter by category. Read the source's description and compatibility guidance.
3. Choose **Install** for the source you want.
4. Wait for the download to finish.
5. Open **Installed** and check its status, entry count, and update time.
6. Open **Protection**, edit Home or a custom setup, select the list, and save.

Start with one general-purpose source and add specialized lists as needed. This makes unexpected blocking easier to trace.

### Packettale

In **Available**, choose **Packettale** in the **Provider** filter. Search for a topic such as phishing, smart TV tracking, or a regional ad filter. Install the categories you want, then select them in the protection setup for the intended devices.

Packettale also offers bundles that combine several categories. One bundle can cover the same domains as its individual feeds or an existing list such as OISD Small. Whole-service lists such as Google services or remote access can disable those services for devices using that protection. [Packettale feed catalog](https://blocklist.packettale.com/feeds.md) · [Upstream sources](https://blocklist.packettale.com/sources.md)

Faro’s Packettale catalog includes published domain feeds that fit its 64 MiB download limit. IP-only feeds belong on a firewall, and Packettale’s all-categories download exceeds the limit.

## Add your own source

1. Choose **Add custom** on **Protection → Blocklists**.
2. Enter a name and the public URL of a hosts file or plain domain list.
3. Leave **Enable after installation** selected if you want it available immediately.
4. Choose **Add blocklist**.
5. Check it under **Installed**, then assign it under **Protection**.

Browser-extension filter lists can contain rules DNS cannot use. Use a hosts file or plain domain list.

## Update, pause, or remove a source

1. Open **Protection → Blocklists → Installed**.
2. Use **Update now** on a source to download it again.
3. Use its pause/resume control to disable or re-enable it.
4. To delete it, choose **Remove** and review the affected protection setups before confirming.

A shared source affects every setup using it. To change only one setup, deselect the list within that protection instead.

**You are done when:** the list is installed, enabled, and selected in the intended protection. Make a fresh lookup for one of its domains and inspect the result in **Activity**.
