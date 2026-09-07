# Choose upstream DNS providers

[All guides](README.md) · Related: [Troubleshooting](troubleshooting.md)

Upstream providers answer lookups Faro cannot answer locally.

## Choose and save providers

1. Open **Upstreams**.
2. Under **Connection to DNS providers**, choose **Encrypted** or **Standard DNS**.
3. Select the provider profiles you want.
4. Review **Current selection** and any compatibility or mixed-filtering messages.
5. Choose **Save upstreams** and wait for confirmation.
6. Choose **Refresh** under live latency to check providers.
7. Make a DNS lookup from a device and check that it succeeds.

| Mode | Use it when… | What it does |
| --- | --- | --- |
| Encrypted | Providers have supported HTTPS endpoints. | Sends public lookups from Faro over DNS-over-HTTPS. |
| Standard DNS | You need a custom IP resolver or ordinary DNS compatibility. | Sends lookups using standard DNS. |

Encryption covers **Faro to the provider**. Devices still reach Faro on port 53 over TCP or UDP. The provider still processes your lookups.

If an encrypted provider fails, Faro can try another selected encrypted provider. It never silently falls back to plaintext. Select more than one supported provider if you want this alternative.

## Add a custom resolver

1. Choose **Standard DNS**.
2. Enter server addresses under **Custom resolvers**.
3. Choose **Add servers**.
4. Review the selection and choose **Save upstreams**.

Custom IP resolvers without a supported HTTPS endpoint cannot be used in encrypted mode. Faro explains incompatible selections before saving.

## Understand provider filtering

Some provider profiles also block categories. Mixing filtered and unfiltered providers can give different results depending on which answers. Choose profiles with compatible behavior and use [Protection](protection.md) for local rules.

Faro's allow exceptions cannot undo a block performed by an upstream. If a request is allowed locally but fails, inspect the provider profile too.
