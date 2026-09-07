# Add device recognition rules

[All guides](README.md) · Before this: [Development](development.md)

This is an advanced contributor guide. To correct a single device, use [Edit device](devices.md#name-a-device) instead.

Recognition definitions live in [internal/devicecatalog/catalog.json](../internal/devicecatalog/catalog.json). Faro combines names and distinctive DNS evidence to suggest a device type. Manual choices take precedence.

## Change the bundled catalog

1. Open the catalog and find the closest existing definition.
2. Add or adjust a narrowly scoped rule. Prefer evidence distinctive to the device over domains shared by many browsers and apps.
3. Increment `catalog_version`.
4. Validate the complete file from the repository root:

   ```sh
   go run ./cmd/faro-device-catalog validate ./internal/devicecatalog/catalog.json
   ```

5. Add regression coverage for both the intended match and a plausible false positive.
6. Run:

   ```sh
   go test ./internal/devicecatalog ./internal/api/handlers
   ```

7. Inspect a matching device in the running app and review **How Faro recognizes this device**.

**You are done when:** validation and relevant tests pass, the intended device is recognized, and the false-positive case is not incorrectly classified.

## Try a custom catalog without rebuilding

1. Save the complete edited catalog locally as `device-catalog.json` and validate it with the command above, changing the path to your file.
2. Keep a copy of any custom catalog already installed.
3. From the production Compose folder, copy the validated file:

   ```sh
   docker compose cp ./device-catalog.json faro:/config/device-catalog.json
   ```

4. Allow the background classifier time to apply it, then inspect device evidence in the interface.

Faro checks the custom file while running. If it becomes invalid, it keeps the last accepted catalog. The authenticated `GET /api/device-catalog` endpoint reports the active source, version, definition count, and any validation error.

To return to the bundled catalog, remove only the custom file after preserving any changes you want:

```sh
docker compose exec faro rm /config/device-catalog.json
```

Then check the reported catalog source again. Development uses `/data/device-catalog.json` by default; a deployment with `FARO_DEVICE_CATALOG_PATH` set uses that configured path.
