package devices

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"net"
	"os"
	"sort"
	"strings"

	"github.com/derek/faro/internal/db"
)

var ErrInvalidAddress = errors.New("invalid device address")

type Identifier struct {
	Kind       string
	Value      string
	Source     string
	Confidence string
}

// ResolveAddress records an address observation and returns Faro's stable local
// device ID. It uses only strong, passive local evidence (an exact Local DNS
// record or an ARP/neighbor MAC) to connect a new address to an existing device.
func ResolveAddress(ctx context.Context, store *db.Store, address, source string) (int64, error) {
	address = normalizeAddress(address)
	if address == "" {
		return 0, ErrInvalidAddress
	}
	if strings.TrimSpace(source) == "" {
		source = "dns"
	}
	identifiers, err := passiveIdentifiers(ctx, store.DB, address)
	if err != nil {
		return 0, err
	}

	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var deviceID int64
	matchedDeviceID, conflictingIdentifiers, err := deviceForIdentifiers(ctx, tx, identifiers)
	if err != nil {
		return 0, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, address).Scan(&deviceID); err == sql.ErrNoRows {
		deviceID = matchedDeviceID
		if deviceID == 0 {
			result, insertErr := tx.ExecContext(ctx, `INSERT INTO devices(first_seen_at, last_seen_at) VALUES(CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
			if insertErr != nil {
				return 0, insertErr
			}
			deviceID, err = result.LastInsertId()
			if err != nil {
				return 0, err
			}
		}
		family := "ipv4"
		if strings.Contains(address, ":") {
			family = "ipv6"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO device_addresses(device_id, address, family, source, confidence)
			VALUES(?, ?, ?, ?, 'observed')`, deviceID, address, family, source); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	if !conflictingIdentifiers {
		for _, identifier := range identifiers {
			resolvedID, observeErr := observeIdentifier(ctx, tx, deviceID, identifier)
			if observeErr != nil {
				return 0, observeErr
			}
			deviceID = resolvedID
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_addresses SET last_seen_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE address = ?`, address); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, deviceID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deviceID, nil
}

func DeviceIDForAddress(ctx context.Context, store *db.Store, address string) (int64, bool, error) {
	address = normalizeAddress(address)
	if address == "" {
		return 0, false, ErrInvalidAddress
	}
	var id int64
	err := store.DB.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, address).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return id, err == nil, err
}

func Addresses(ctx context.Context, store *db.Store, deviceID int64) ([]string, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT address FROM device_addresses WHERE device_id = ? ORDER BY last_seen_at DESC, id DESC`, deviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, err
		}
		result = append(result, address)
	}
	return result, rows.Err()
}

func AssignProtection(ctx context.Context, store *db.Store, address string, protectionID int64) (int64, error) {
	deviceID, err := ResolveAddress(ctx, store, address, "assignment")
	if err != nil {
		return 0, err
	}
	if _, err := store.DB.ExecContext(ctx, `
		INSERT INTO device_protection_memberships(device_id, protection_id, updated_at)
		VALUES(?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(device_id) DO UPDATE SET protection_id = excluded.protection_id, updated_at = CURRENT_TIMESTAMP`, deviceID, protectionID); err != nil {
		return 0, err
	}
	return deviceID, nil
}

func RemoveProtection(ctx context.Context, store *db.Store, address string) error {
	deviceID, ok, err := DeviceIDForAddress(ctx, store, address)
	if err != nil || !ok {
		return err
	}
	_, err = store.DB.ExecContext(ctx, `DELETE FROM device_protection_memberships WHERE device_id = ?`, deviceID)
	return err
}

func passiveIdentifiers(ctx context.Context, database *sql.DB, address string) ([]Identifier, error) {
	var identifiers []Identifier
	rows, err := database.QueryContext(ctx, `
		SELECT DISTINCT LOWER(TRIM(hostname))
		FROM dns_records
		WHERE value = ? AND type IN ('A', 'AAAA') AND TRIM(hostname) <> ''`, address)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var hostname string
		if err := rows.Scan(&hostname); err != nil {
			_ = rows.Close()
			return nil, err
		}
		identifiers = append(identifiers, Identifier{Kind: "local_dns", Value: strings.TrimSuffix(hostname, "."), Source: "local_dns", Confidence: "strong"})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if mac := arpMAC(address); mac != "" {
		identifiers = append(identifiers, Identifier{Kind: "mac", Value: mac, Source: "neighbor_table", Confidence: "strong"})
	}
	sort.Slice(identifiers, func(i, j int) bool { return identifiers[i].Kind < identifiers[j].Kind })
	return identifiers, nil
}

func deviceForIdentifiers(ctx context.Context, tx *sql.Tx, identifiers []Identifier) (int64, bool, error) {
	var found int64
	for _, identifier := range identifiers {
		var candidate int64
		err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_identifiers WHERE kind = ? AND value = ?`, identifier.Kind, identifier.Value).Scan(&candidate)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return 0, false, err
		}
		if found != 0 && found != candidate {
			// Conflicting strong signals should never trigger an automatic merge.
			return 0, true, nil
		}
		found = candidate
	}
	return found, false, nil
}

func observeIdentifier(ctx context.Context, tx *sql.Tx, deviceID int64, identifier Identifier) (int64, error) {
	identifier.Kind = strings.ToLower(strings.TrimSpace(identifier.Kind))
	identifier.Value = strings.ToLower(strings.TrimSpace(identifier.Value))
	if identifier.Kind == "" || identifier.Value == "" {
		return deviceID, nil
	}
	var existingID int64
	err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_identifiers WHERE kind = ? AND value = ?`, identifier.Kind, identifier.Value).Scan(&existingID)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if err == nil && existingID != deviceID {
		merged, mergeErr := safeMerge(ctx, tx, existingID, deviceID)
		if mergeErr != nil {
			return 0, mergeErr
		}
		if !merged {
			return deviceID, nil
		}
		deviceID = existingID
	}
	if identifier.Confidence == "" {
		identifier.Confidence = "observed"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO device_identifiers(device_id, kind, value, source, confidence)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(kind, value) DO UPDATE SET last_seen_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP`,
		deviceID, identifier.Kind, identifier.Value, identifier.Source, identifier.Confidence)
	return deviceID, err
}

func safeMerge(ctx context.Context, tx *sql.Tx, targetID, sourceID int64) (bool, error) {
	if targetID == sourceID {
		return true, nil
	}
	type deviceFields struct {
		name, location, notes string
		confirmed             bool
		protection            sql.NullInt64
	}
	read := func(id int64) (deviceFields, error) {
		var fields deviceFields
		err := tx.QueryRowContext(ctx, `
			SELECT d.name, COALESCE(d.location, ''), COALESCE(d.notes, ''), d.confirmed, m.protection_id
			FROM devices d LEFT JOIN device_protection_memberships m ON m.device_id = d.id
			WHERE d.id = ?`, id).Scan(&fields.name, &fields.location, &fields.notes, &fields.confirmed, &fields.protection)
		return fields, err
	}
	target, err := read(targetID)
	if err != nil {
		return false, err
	}
	source, err := read(sourceID)
	if err != nil {
		return false, err
	}
	if target.confirmed && source.confirmed && strings.TrimSpace(target.name) != "" && !strings.EqualFold(target.name, source.name) {
		return false, nil
	}
	if target.protection.Valid && source.protection.Valid && target.protection.Int64 != source.protection.Int64 {
		return false, nil
	}
	if !target.protection.Valid && source.protection.Valid {
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?)`, targetID, source.protection.Int64); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE devices SET
			name = CASE WHEN TRIM(name) = '' THEN ? ELSE name END,
			location = CASE WHEN COALESCE(TRIM(location), '') = '' THEN NULLIF(?, '') ELSE location END,
			notes = CASE WHEN COALESCE(TRIM(notes), '') = '' THEN NULLIF(?, '') ELSE notes END,
			confirmed = CASE WHEN confirmed = 1 OR ? = 1 THEN 1 ELSE 0 END,
			first_seen_at = MIN(COALESCE(first_seen_at, CURRENT_TIMESTAMP), COALESCE((SELECT first_seen_at FROM devices WHERE id = ?), CURRENT_TIMESTAMP)),
			last_seen_at = MAX(COALESCE(last_seen_at, ''), COALESCE((SELECT last_seen_at FROM devices WHERE id = ?), '')),
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, source.name, source.location, source.notes, source.confirmed, sourceID, sourceID, targetID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_addresses SET device_id = ?, updated_at = CURRENT_TIMESTAMP WHERE device_id = ?`, targetID, sourceID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dns_queries SET device_id = ? WHERE device_id = ?`, targetID, sourceID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_identifiers WHERE device_id = ? AND EXISTS (SELECT 1 FROM device_identifiers target WHERE target.device_id = ? AND target.kind = device_identifiers.kind AND target.value = device_identifiers.value)`, sourceID, targetID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_identifiers SET device_id = ?, updated_at = CURRENT_TIMESTAMP WHERE device_id = ?`, targetID, sourceID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_protection_memberships WHERE device_id = ?`, sourceID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, sourceID); err != nil {
		return false, err
	}
	return true, nil
}

func normalizeAddress(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "[]"))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if zone := strings.LastIndex(value, "%"); zone >= 0 {
		value = value[:zone]
	}
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func arpMAC(address string) string {
	if !strings.Contains(address, ".") {
		return ""
	}
	file, err := os.Open("/proc/net/arp")
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] != address {
			continue
		}
		mac, err := net.ParseMAC(fields[3])
		if err == nil && fields[3] != "00:00:00:00:00:00" {
			return strings.ToLower(mac.String())
		}
	}
	return ""
}
