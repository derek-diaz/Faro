# Install Faro on Unraid

[All guides](README.md) · Next: [Connect devices](connect-devices.md)

Faro uses the same Docker image on Unraid as on other systems. Its template fills in the container's ports and data folder.

## 1. Add the template

1. Open the Unraid terminal from the web interface.
2. Download the repository's template:

   ```sh
   mkdir -p /boot/config/plugins/dockerMan/templates-user
   curl -fL https://raw.githubusercontent.com/derek-diaz/Faro/main/templates/faro.xml -o /boot/config/plugins/dockerMan/templates-user/my-faro.xml
   ```

3. Open **Docker → Add Container**.
4. Select **Faro** from the template list. Refresh the page if needed.

Unraid stores Docker application settings in this user-template folder. See [Unraid's application documentation](https://docs.unraid.net/unraid-os/manual/applications/) for background.

## 2. Review the settings

| Setting | What to use |
| --- | --- |
| Repository | `tabierto/faro:latest` to follow releases, or a specific published version to pin it. The repository template may arrive with a version already pinned. |
| Application Data | `/mnt/user/appdata/faro` on the host, mapped to `/config` in the container. |
| Web Interface | Host port `1787` to container port `1787`, TCP. |
| DNS | Host port `53` to container port `53`, with separate TCP and UDP mappings. |

For **bridge** networking, Faro uses the Unraid host's LAN address. Reserve it in your router.

If the host already uses port 53, choose an existing custom LAN network and give Faro its own unused fixed IP. Use that container IP and its internal ports: `53` for DNS and `1787` for the interface. Host port remapping does not change these ports in custom-IP mode.

## 3. Start and test

1. Choose **Apply** and wait for the container to start.
2. Open `http://YOUR-FARO-IP:1787`.
3. Create your administrator account and complete setup.
4. From another device, run `nslookup example.com YOUR-FARO-IP`.
5. Look for the request in **Activity**.

**You are done when:** Faro answers a direct lookup. Follow [Connect devices](connect-devices.md) to use it across your network.

## Update and back up

1. [Download a Faro backup](backup-restore.md).
2. On Unraid's **Docker** page, check for updates and apply the Faro update.
3. If the repository uses a pinned tag, edit that tag to the intended published version first. Checking for updates alone does not move a pinned installation to the next version.
4. Repeat the DNS test.

Keep the `/config` mapping across updates. Include the appdata folder in your normal Unraid backups; stop Faro before making a plain file copy.
