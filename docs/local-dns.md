# Give a local server an easy name

[All guides](README.md) · Related: [DNS settings](settings.md)

For example, `media.home` can point to a server at `192.168.1.22`.

## Add a record

1. Reserve the server's IP in your router so it stays the same.
2. Open **Local DNS → Add local record**.
3. Enter a **Hostname**. Entering `media` adds your configured suffix; check the field's hint.
4. Choose **A** for IPv4 or **AAAA** for IPv6.
5. Enter the address in **Value**, without `http://` or a port.
6. Optionally add a description, then choose **Add record**.

## Test it

From a device using Faro, run:

```sh
nslookup media.home 192.168.1.10
```

Replace the name and Faro address with yours. The answer should contain the server's IP.

Open the service using its usual protocol and port, for example `http://media.home:8096`. DNS does not create a web service or change its port or certificate.

## Edit or remove it

1. Find the record under **Local DNS records**.
2. Choose **Edit**, change the fields, and choose **Save record**.
3. To remove it, choose **Delete**.
4. Repeat the lookup. Cached answers can remain until they expire.

Change the default suffix under **Settings → DNS & interface → Local domain suffix**. Inspect existing records separately; this setting is not a bulk rename tool.
