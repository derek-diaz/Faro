# Set up protection and exceptions

[All guides](README.md) · Related: [Manage blocklists](blocklists.md)

A setup combines blocklists, exact-domain exceptions, and a blocking schedule. **Home** is the default for devices without another assignment.

## Change protection for your home

1. Open **Protection** and select **Home**.
2. Choose **Edit**.
3. Select the installed blocklists you want.
4. Select **Preview rule changes** to see what Faro would start or stop blocking and which devices would be affected. Expand a domain group to see the most frequent matches.
5. Review the schedule and choose **Save changes** when ready.

The preview compares saved and proposed rules against up to the latest 20,000 requests in the past 24 hours from devices currently covered by the setup. It assumes blocking is on and honors active temporary exceptions. Counts refer to DNS requests, so repeated lookups of one domain count separately.

Check a fresh lookup from a device using Home. Devices assigned elsewhere use their own setup.

## Create a setup for selected devices

1. Choose **New protection setup**.
2. Enter a name and choose **Standard**, **Stricter**, or **Custom**. Standard uses OISD Small for everyday blocking; Stricter uses HaGeZi Normal for broader privacy coverage. The icon is optional.
3. For Custom, select installed lists or add a source name and URL. You can also use only domain exceptions.
4. Select observed devices, or assign them later. Expand **Domain exceptions** if you need exact-domain allow or block rules.
5. Review the affected devices and lists, then choose **Create protection**. Faro installs any required source at this point and adds it to this setup.

New setups block domains all day. Edit the setup afterward to add a schedule. If a preset's list is disabled and already belongs to another setup, choose another option or review its effect in Blocklists before enabling it.

If a download or creation fails, your choices remain in the wizard. Choose **Try again** after checking the error. Sources installed during the attempt remain available in Blocklists and are reused on retry.

You can also assign a setup from the **Devices** page.

When Faro recognizes a new address as belonging to an assigned device, it updates DNS protection automatically on its next 15-second policy check. A reused address with a different hardware identity belongs to the new device; the previous device keeps its history and assignments.

## Allow or block a domain

1. Edit the protection used by the affected device.
2. Find **Exceptions**.
3. Add the domain under **Always allow** or **Always block**.
4. Enter only the domain, such as `ads.example.net`, without `https://` or a page path.
5. Save and test a fresh lookup.

Exceptions belong to that protection and match exact domains. Add required subdomains separately. For a broken site, [test temporary exceptions first](fix-a-site.md).

## Set blocking hours

1. Edit the protection and open **Schedule**.
2. Select **Block only during selected hours**.
3. Set the blocking days, start time, and end time. Check the displayed time zone.
4. Save and check the setup's active state.

These are the hours when **blocking is on**. Outside them, devices remain online and public domains are allowed; local DNS still works. Choose **Block domains all the time** for continuous protection.

## Pause blocking

1. Select the setup.
2. Choose a pause duration, such as **For 5 min** or **For 1 hour**.
3. Choose **Start blocking again** to end the pause early.

This affects all devices using that setup and keeps them online. To restrict a device's DNS access, use [Device internet pause](devices.md#temporarily-pause-dns-access).

## Delete a custom setup

1. Edit the custom setup and choose its delete action.
2. Review the affected devices and confirm **Delete protection**.
3. Check those devices under Home, where they now fall back.

Home cannot be deleted. Removing a custom setup deletes its exceptions but keeps devices and history.
