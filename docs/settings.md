# Change Faro settings

[All guides](README.md) · Related: [Docker configuration](configuration.md) · [Storage](storage.md)

Most changes happen in **Settings**. Provider selection has its own **Upstreams** page.

## Change DNS behavior

1. Open **Settings → DNS & interface**.
2. Change the setting you need:

   | Setting | What it changes |
   | --- | --- |
   | Local domain suffix | The suffix suggested for new local DNS records. |
   | Faro LAN address | The address Faro displays as its DNS server address. This does not change the host's network configuration. |
   | DNS response cache | Whether Faro keeps repeated answers locally, and the configured cache lifetime. |
   | Domain favicons | Whether Faro downloads icons for public domains shown in the interface. |

3. Choose **Save changes** and wait for confirmation.
4. Make a fresh DNS lookup and check **Activity**.

Favicons are off by default. Enabling them makes outbound requests based on observed domains and stores downloaded icons locally. Local names keep their initials.

Changing Faro's recorded LAN address does not update your router or devices. If the machine's address changes, update [their DNS settings](connect-devices.md) too.

## Allow an additional network range

Most home and private networks work without changes.

1. Open **Settings → DNS & interface → Network access**.
2. Choose **Advanced**.
3. Add the routed network prefix that needs access, using the format requested by the field.
4. Save and test a lookup from that network.

Only add networks you intend to serve. This setting controls DNS client access; host firewall and routing rules still need to allow the traffic.

## Change appearance

1. Choose the appearance button in the top bar.
2. Select **Light**, **Dark**, or **System**.

The choice is saved in that browser. System follows your device's appearance preference.

## Change your password

1. Open **Settings → Account**.
2. Enter your current password.
3. Enter and confirm a new password of at least eight characters.
4. Choose **Change password**.

Other sessions are signed out. This password is separate from a backup's encryption passphrase.

## Inspect or reload DNS

1. Open **Settings → Advanced** to inspect **CoreDNS configuration** and the active files.
2. Review any reported differences or errors.
3. Make changes through the normal Faro pages. The file view is read-only.
4. If you need to request a reload, use **Settings → DNS & interface → Reload DNS** and check the result.

A reload is not a factory reset. If it fails, read the message and follow [troubleshooting](troubleshooting.md).
