# Add a second Faro DNS server

[All guides](README.md) · Before this: [Install Faro](installation.md) · Related: [Backups](backup-restore.md)

One primary server manages settings. Additional servers, called replicas, answer DNS using the primary's accepted configuration.

## Before you start

- Give each machine a different fixed LAN IP.
- Install the same Faro application on the additional machine.
- Make sure the replica can reach the primary's local web address.
- Finish or undo temporary broken-site tests before pairing.

## 1. Create a pairing code

1. On the primary, open **Settings → Redundancy**.
2. Choose **Set up redundancy**, or **Add another server** if redundancy is already enabled.
3. Copy the temporary pairing code.

The code expires after ten minutes and can be used once. Create another if it expires.

## 2. Join the new server

1. Open the new Faro server's web interface.
2. Choose **Join an existing Faro home** on its setup, sign-in, or initial configuration screen.
3. Enter the primary's private URL, such as `http://192.168.1.10:1787`.
4. Enter the code, a recognizable server name, and the new server's LAN IP.
5. Complete pairing and wait for synchronization.

If you already created an administrator on the new server, use the available join action rather than deleting its data folder.

## 3. Verify before changing the router

1. Open **Settings → Redundancy** on the primary.
2. Check that the replica is healthy and its configuration revision matches the primary.
3. Run a DNS lookup against each server separately:

   ```sh
   nslookup example.com 192.168.1.10
   nslookup example.com 192.168.1.11
   ```

4. Test a local record and a domain your protection blocks against both servers.
5. Only then advertise both IPs through your router's LAN/DHCP DNS settings and reconnect clients.

Use your actual server addresses. A direct lookup against each server confirms they both work; client failover behavior still depends on the router and device.

## What happens if the primary goes offline

Replicas continue answering with their last accepted configuration. They do not become primary automatically. Settings changes wait until the primary returns.

The primary's activity view does not merge replica query history. A device using a replica may therefore be absent from the primary's recent activity.

## Remove an additional server

1. Remove that server's DNS address from your router and any manually configured clients.
2. Reconnect clients so they receive the remaining DNS address.
3. On the primary, remove the server under **Settings → Redundancy** and confirm.
4. On the removed server, choose **Leave Faro home** before reusing it, or stop it if retiring it.

Removing a server from the primary stops updates; it may still answer using old settings until stopped or reconfigured.

## Turn off redundancy

1. Remove additional-server addresses from DHCP and manual client settings.
2. On the primary, choose **Turn off redundancy** and confirm.
3. Open each additional server and choose **Leave Faro home**, or stop it.
4. Test DNS against the remaining standalone Faro server.

Portable backups do not transfer pairing secrets. Pair replicas again after restoring a primary onto a fresh installation.
