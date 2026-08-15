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

type deviceFields struct {
	name, location, notes string
	confirmed             bool
	protection            sql.NullInt64
}

// ResolveAddress records an address observation and returns Faro's stable local
// device ID. It uses only strong, passive local evidence (an exact Local DNS
// record or an ARP/neighbor MAC) to connect a new address to an existing device.
func ResolveAddress(ctx context.Context, store *db.Store, address, source string) (int64, error) {
	return ObserveAddress(ctx, store, address, source, nil)
}

// ObserveAddress records an address together with strong identifiers supplied
// by a trusted local integration. Explicit identifiers are combined with
// Faro's passive evidence and pass through the same conflict-safe merge rules.
func ObserveAddress(ctx context.Context, store *db.Store, address, source string, explicit []Identifier) (deviceID int64, err error) {
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
	identifiers = append(identifiers, explicit...)
	identifiers = normalizedIdentifiers(identifiers)

	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, rollbackErr)
		}
	}()

	matchedDeviceID, conflictingIdentifiers, err := deviceForIdentifiers(ctx, tx, identifiers)
	if err != nil {
		return 0, err
	}
	deviceID, err = deviceForAddress(ctx, tx, address, source, matchedDeviceID)
	if err != nil {
		return 0, err
	}

	if !conflictingIdentifiers {
		deviceID, err = observeIdentifiers(ctx, tx, deviceID, identifiers)
		if err != nil {
			return 0, err
		}
	}
	if err = touchObservation(ctx, tx, address, deviceID); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return deviceID, nil
}

func deviceForAddress(ctx context.Context, tx *sql.Tx, address, source string, matchedDeviceID int64) (int64, error) {
	var deviceID int64
	err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, address).Scan(&deviceID)
	if err == nil {
		return deviceID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	deviceID = matchedDeviceID
	if deviceID == 0 {
		result, err := tx.ExecContext(ctx, `INSERT INTO devices(first_seen_at, last_seen_at) VALUES(strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`)
		if err != nil {
			return 0, err
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
		INSERT INTO device_addresses(device_id, address, family, source, confidence, first_seen_at, last_seen_at)
		VALUES(?, ?, ?, ?, 'observed', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`, deviceID, address, family, source); err != nil {
		return 0, err
	}
	return deviceID, nil
}

func observeIdentifiers(ctx context.Context, tx *sql.Tx, deviceID int64, identifiers []Identifier) (int64, error) {
	for _, identifier := range identifiers {
		resolvedID, err := observeIdentifier(ctx, tx, deviceID, identifier)
		if err != nil {
			return 0, err
		}
		deviceID = resolvedID
	}
	return deviceID, nil
}

func touchObservation(ctx context.Context, tx *sql.Tx, address string, deviceID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE device_addresses SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), updated_at = CURRENT_TIMESTAMP WHERE address = ?`, address); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), updated_at = CURRENT_TIMESTAMP WHERE id = ?`, deviceID)
	return err
}

func normalizedIdentifiers(input []Identifier) []Identifier {
	result := make([]Identifier, 0, len(input))
	seen := map[string]bool{}
	for _, identifier := range input {
		identifier.Kind = strings.ToLower(strings.TrimSpace(identifier.Kind))
		identifier.Value = strings.ToLower(strings.TrimSpace(identifier.Value))
		if identifier.Kind == "" || identifier.Value == "" {
			continue
		}
		key := identifier.Kind + "\x00" + identifier.Value
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, identifier)
	}
	sort.Slice(result, func(leftIndex, rightIndex int) bool {
		if result[leftIndex].Kind == result[rightIndex].Kind {
			return result[leftIndex].Value < result[rightIndex].Value
		}
		return result[leftIndex].Kind < result[rightIndex].Kind
	})
	return result
}

func DeviceIDForAddress(ctx context.Context, store *db.Store, address string) (int64, bool, error) {
	address = normalizeAddress(address)
	if address == "" {
		return 0, false, ErrInvalidAddress
	}
	var id int64
	err := store.DB.QueryRowContext(ctx, `SELECT device_id FROM device_addresses WHERE address = ?`, address).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

func Addresses(ctx context.Context, store *db.Store, deviceID int64) (result []string, err error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT address FROM device_addresses WHERE device_id = ? ORDER BY last_seen_at DESC, id DESC`, deviceID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, err
		}
		result = append(result, address)
	}
	return result, rows.Err()
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
	sort.Slice(identifiers, func(leftIndex, rightIndex int) bool {
		return identifiers[leftIndex].Kind < identifiers[rightIndex].Kind
	})
	return identifiers, nil
}

func deviceForIdentifiers(ctx context.Context, tx *sql.Tx, identifiers []Identifier) (int64, bool, error) {
	var found int64
	for _, identifier := range identifiers {
		var candidate int64
		err := tx.QueryRowContext(ctx, `SELECT device_id FROM device_identifiers WHERE kind = ? AND value = ?`, identifier.Kind, identifier.Value).Scan(&candidate)
		if errors.Is(err, sql.ErrNoRows) {
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
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
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
	target, err := readDeviceFields(ctx, tx, targetID)
	if err != nil {
		return false, err
	}
	source, err := readDeviceFields(ctx, tx, sourceID)
	if err != nil {
		return false, err
	}
	if !canMergeDevices(target, source) {
		return false, nil
	}
	if err := mergeProtection(ctx, tx, targetID, target.protection, source.protection); err != nil {
		return false, err
	}
	if err := mergeDeviceData(ctx, tx, targetID, sourceID, source); err != nil {
		return false, err
	}
	return true, nil
}

func readDeviceFields(ctx context.Context, tx *sql.Tx, deviceID int64) (deviceFields, error) {
	var fields deviceFields
	err := tx.QueryRowContext(ctx, `
		SELECT d.name, COALESCE(d.location, ''), COALESCE(d.notes, ''), d.confirmed, m.protection_id
		FROM devices d LEFT JOIN device_protection_memberships m ON m.device_id = d.id
		WHERE d.id = ?`, deviceID).Scan(&fields.name, &fields.location, &fields.notes, &fields.confirmed, &fields.protection)
	return fields, err
}

func canMergeDevices(target, source deviceFields) bool {
	if target.confirmed && source.confirmed && strings.TrimSpace(target.name) != "" && !strings.EqualFold(target.name, source.name) {
		return false
	}
	if target.protection.Valid && source.protection.Valid && target.protection.Int64 != source.protection.Int64 {
		return false
	}
	return true
}

func mergeProtection(ctx context.Context, tx *sql.Tx, targetID int64, target, source sql.NullInt64) error {
	if target.Valid || !source.Valid {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO device_protection_memberships(device_id, protection_id) VALUES(?, ?)`, targetID, source.Int64)
	return err
}

func mergeDeviceData(ctx context.Context, tx *sql.Tx, targetID, sourceID int64, source deviceFields) error {
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
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_addresses SET device_id = ?, updated_at = CURRENT_TIMESTAMP WHERE device_id = ?`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE dns_queries SET device_id = ? WHERE device_id = ?`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_identifiers WHERE device_id = ? AND EXISTS (SELECT 1 FROM device_identifiers target WHERE target.device_id = ? AND target.kind = device_identifiers.kind AND target.value = device_identifiers.value)`, sourceID, targetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_identifiers SET device_id = ?, updated_at = CURRENT_TIMESTAMP WHERE device_id = ?`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_names WHERE device_id = ? AND source IN (SELECT source FROM device_names WHERE device_id = ?)`, sourceID, targetID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE device_names SET device_id = ?, updated_at = CURRENT_TIMESTAMP WHERE device_id = ?`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE unifi_client_snapshots SET device_id = ? WHERE device_id = ?`, targetID, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_protection_memberships WHERE device_id = ?`, sourceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, sourceID); err != nil {
		return err
	}
	return nil
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
	var result string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] != address {
			continue
		}
		mac, err := net.ParseMAC(fields[3])
		if err == nil && fields[3] != "00:00:00:00:00:00" {
			result = strings.ToLower(mac.String())
			break
		}
	}
	if scanner.Err() != nil {
		_ = file.Close()
		return ""
	}
	if err := file.Close(); err != nil {
		return ""
	}
	return result
}
