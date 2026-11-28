package handlers

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"

	deviceidentity "github.com/derek/faro/internal/devices"
)

// Stable references keep an open panel attached to its device after DHCP reuse.
// IP references remain supported for existing links and API clients.
func (handler *Handler) resolveDeviceReference(ctx context.Context, reference, source string) (int64, string, error) {
	if rawID, stable := strings.CutPrefix(reference, "id:"); stable {
		id, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || id <= 0 {
			return 0, "", errors.New("invalid device id")
		}
		var address string
		err = handler.store.DB.QueryRowContext(ctx, `SELECT COALESCE((
			SELECT address FROM device_addresses WHERE device_id = devices.id ORDER BY last_seen_at DESC, id DESC LIMIT 1
		), '') FROM devices WHERE id = ?`, id).Scan(&address)
		if err != nil {
			return 0, "", errors.New("device was not found")
		}
		return id, address, nil
	}
	ip := net.ParseIP(reference)
	if ip == nil {
		return 0, "", errors.New("invalid device address")
	}
	reference = ip.String()
	if source != "" {
		id, err := deviceidentity.ResolveAddress(ctx, handler.store, reference, source)
		return id, reference, err
	}
	id, found, err := deviceidentity.DeviceIDForAddress(ctx, handler.store, reference)
	if err != nil {
		return 0, "", err
	}
	if !found {
		return 0, "", errors.New("device was not found")
	}
	return id, reference, nil
}

func (handler *Handler) deviceIdentity(ctx context.Context, deviceID int64, addresses []string, fallback string) deviceIdentity {
	identity := bestDeviceIdentity(handler.discoverDeviceNames(ctx, addresses), addresses, fallback)
	var name string
	if handler.store.DB.QueryRowContext(ctx, `SELECT name FROM device_names WHERE device_id = ? AND source = 'unifi' AND TRIM(name) <> ''`, deviceID).Scan(&name) == nil {
		identity.DisplayName, identity.NameSource = name, "unifi"
	}
	return identity
}
