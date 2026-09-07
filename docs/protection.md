# Set up protection and exceptions

[All guides](README.md) · Before this: [Install blocklists](blocklists.md)

A setup combines blocklists, exact-domain exceptions, and a blocking schedule. **Home** is the default for devices without another assignment.

## Change protection for your home

1. Open **Protection** and select **Home**.
2. Choose **Edit**.
3. Select the installed blocklists you want.
4. Review exceptions and the schedule.
5. Choose **Save changes** and wait for confirmation.

Check a fresh lookup from a device using Home. Devices assigned elsewhere use their own setup.

## Create a setup for selected devices

1. Choose **New protection setup**.
2. Enter a name and choose an icon.
3. Select installed blocklists.
4. Add any exact-domain exceptions you need.
5. Select observed devices, or assign them later.
6. Review and choose **Create protection**.

You can also assign a setup from the **Devices** page.

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
