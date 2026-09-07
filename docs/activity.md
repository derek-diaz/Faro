# Explore DNS activity

[All guides](README.md) · Related: [Devices](devices.md) · [Fix a broken site](fix-a-site.md)

Use **Dashboard** for an overview and **Activity** to investigate requests.

## Check your network

1. Open **Dashboard**.
2. Review today's requests, blocks, active devices, and cache hit rate.
3. Check upstream resolver status and response times.
4. Select a domain or device to inspect it.

![Dashboard with example data](screenshots/dashboard.svg)

## Find a request

1. Open **Activity**.
2. Choose a time range above the timeline.
3. Enter a domain, device, or event in the search box and choose **Search**.
4. Select **Blocked**, **Cache**, **Upstream**, or another filter.
5. Select a domain in the table to inspect its current decision and recent requests.
6. Clear the search and return to **All** for the wider view.

![Activity with example data](screenshots/activity.svg)

## Read the results

| Result | Meaning |
| --- | --- |
| Allowed | Faro did not block the request. This does not prove the website loaded. |
| Blocked | Faro applied a blocking decision. Inspect the domain for the reason. |
| Cache | Faro answered using a cached response. |
| Upstream | The request went to an upstream DNS service. |
| A / AAAA | A lookup for an IPv4 / IPv6 address. |
| System | A Faro event rather than an ordinary DNS request. |

Historical requests describe what happened at the time. The inspector's current decision describes the rules now. They may differ after protection changes.

DNS activity shows name lookups, not page contents, full browsing URLs, or proof that a device connected to a service. Only requests sent through Faro appear. Totals and the timeline can briefly lag the newest rows while refreshing.

**You are done when:** you can identify the device, result, and source of the request. If blocking broke a site, use [Fix a broken site](fix-a-site.md).

## Search across Faro

1. Choose **Search** in the top bar.
2. Enter a device, domain, event, rule, local DNS record, or blocklist name.
3. Select a result to open it. Close the search panel to return to your page.

This searches more than the Activity table. Use Activity's own filters when you want requests from a specific period.

## Review network updates

1. Choose the notification bell in the top bar to open **Network updates**.
2. Review **Needs attention** and **Recent changes**.
3. Open an item's available action to investigate, or dismiss it after reviewing it.

Dismissing a notice does not fix its underlying cause. Check the relevant page after making a change.
