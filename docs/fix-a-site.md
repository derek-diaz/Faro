# Fix a site or app that stopped working

[All guides](README.md) · Related: [Protection exceptions](protection.md#allow-or-block-a-domain)

Use Faro's guided check to find out whether a blocked domain is causing the problem. A blocked request is a clue, not proof.

## 1. Capture the problem

1. Open **Devices** and select the affected device.
2. Select **Fix a broken site**.
3. Choose **Start fresh capture**.
4. Open the broken site or app on that device and reproduce the problem.
5. Return to Faro and choose **Refresh results**.

Only requests reaching this Faro server appear. If nothing appears, check the device's [DNS settings](connect-devices.md).

## 2. Try a temporary exception

1. Review the blocked domains and failed responses.
2. Select domains that look relevant and are currently blocked.
3. Choose the **Allow … temporarily** button.
4. Retry the site on the affected device.

Tests last ten minutes and allow up to twenty exact domains. **They affect every device using the selected protection**, not just the device being investigated. Subdomains must be selected separately.

DNS and app caches can delay the effect. Closing the panel does not end the test.

## 3. Keep or undo the change

1. If the test fixed the problem, choose **It helped — keep exceptions**.
2. If it did not help, choose **Undo test**.
3. Retry the site and check that the outcome matches your choice.

Keeping the test adds permanent exceptions to the protection where it started and replaces conflicting custom blocks for those domains. Undoing it leaves existing permanent exceptions and other tests alone. Without either action, the temporary test expires automatically.

## If temporary tests are unavailable

Temporary tests require a standalone Faro installation. Servers using redundancy can inspect captures but cannot start these tests. Finish or undo tests before pairing replicas.

In a redundant installation, review the current decision and edit the appropriate [protection exceptions](protection.md) on the primary if needed. Check the effect on all devices using that protection.

If Faro allows the request but it still fails, check [upstream filtering](upstreams.md#understand-provider-filtering) and the service itself.
