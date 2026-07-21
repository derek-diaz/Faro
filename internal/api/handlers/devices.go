package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	deviceidentity "github.com/derek/faro/internal/devices"
)

func (s *Handler) devices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	start := todayStart(r)
	rows, err := s.store.DB.QueryContext(r.Context(), `
		SELECT
			d.id,
			COALESCE((SELECT address FROM device_addresses WHERE device_id = d.id ORDER BY last_seen_at DESC, id DESC LIMIT 1), '') AS client_ip,
			d.name,
			d.location,
			d.notes,
			d.device_type,
			d.type_source,
			COUNT(CASE WHEN q.timestamp >= ? THEN 1 END) AS total_queries_today,
			COALESCE(SUM(CASE WHEN q.timestamp >= ? AND q.action = 'blocked' THEN 1 ELSE 0 END), 0) AS blocked_queries_today,
			COALESCE(MAX(q.timestamp), strftime('%Y-%m-%dT%H:%M:%SZ', d.last_seen_at)) AS last_seen
		FROM devices d
		LEFT JOIN dns_queries q ON q.device_id = d.id
		GROUP BY d.id
		ORDER BY last_seen DESC, client_ip
	`, start, start)
	if err != nil {
		writeError(w, err)
		return
	}
	defer rows.Close()

	type baseDevice struct {
		deviceID   int64
		clientIP   string
		name       string
		location   any
		notes      any
		deviceType string
		typeSource string
		total      int
		blocked    int
		lastSeen   any
	}
	baseDevices := []baseDevice{}
	for rows.Next() {
		var clientIP, name, deviceType, typeSource string
		var location, notes, lastSeen sql.NullString
		var total, blocked int
		var deviceID int64
		if err := rows.Scan(&deviceID, &clientIP, &name, &location, &notes, &deviceType, &typeSource, &total, &blocked, &lastSeen); err != nil {
			writeError(w, err)
			return
		}
		baseDevices = append(baseDevices, baseDevice{
			deviceID:   deviceID,
			clientIP:   clientIP,
			name:       name,
			location:   nullableString(location),
			notes:      nullableString(notes),
			deviceType: deviceType,
			typeSource: typeSource,
			total:      total,
			blocked:    blocked,
			lastSeen:   nullableString(lastSeen),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, err)
		return
	}
	rows.Close()

	items := []map[string]any{}
	clientIPs := make([]string, 0, len(baseDevices)*2)
	addressesByDevice := make(map[int64][]string, len(baseDevices))
	for _, device := range baseDevices {
		addresses, addressErr := deviceidentity.Addresses(r.Context(), s.store, device.deviceID)
		if addressErr != nil {
			writeError(w, addressErr)
			return
		}
		addressesByDevice[device.deviceID] = addresses
		clientIPs = append(clientIPs, addresses...)
	}
	discoveredNames := s.discoverDeviceNames(r.Context(), clientIPs)
	for _, device := range baseDevices {
		addresses := addressesByDevice[device.deviceID]
		identity := bestDeviceIdentity(discoveredNames, addresses, device.clientIP)
		if strings.TrimSpace(device.name) != "" {
			identity.DisplayName = device.name
			identity.NameSource = "manual"
		}
		typeSource := "automatic"
		if device.typeSource == "manual" && validManualDeviceType(device.deviceType) {
			identity.DeviceType, identity.TypeConfidence, typeSource = device.deviceType, "high", "manual"
		} else {
			identity.DeviceType, identity.TypeConfidence = inferDeviceTypeForDevice(r.Context(), s.store.DB, device.deviceID, device.clientIP, identity.DisplayName)
		}
		protectionID, protectionName, protectionIcon := protectionForClient(r.Context(), s.store.DB, device.clientIP)
		items = append(items, map[string]any{
			"device_id":             device.deviceID,
			"client_ip":             device.clientIP,
			"addresses":             addresses,
			"identity_source":       deviceIdentitySource(r.Context(), s.store.DB, device.deviceID, identity.NameSource),
			"name":                  device.name,
			"display_name":          identity.DisplayName,
			"name_source":           identity.NameSource,
			"location":              device.location,
			"notes":                 device.notes,
			"device_type":           identity.DeviceType,
			"type_confidence":       identity.TypeConfidence,
			"type_source":           typeSource,
			"total_queries_today":   device.total,
			"blocked_queries_today": device.blocked,
			"block_percentage":      percentage(device.blocked, device.total),
			"top_domains":           grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 5`, device.deviceID, start),
			"last_seen":             device.lastSeen,
			"profile":               protectionName,
			"protection":            protectionName,
			"protection_id":         protectionID,
			"protection_icon":       protectionIcon,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

func bestDeviceIdentity(discovered map[string]deviceIdentity, addresses []string, fallback string) deviceIdentity {
	for _, address := range addresses {
		if identity := discovered[address]; strings.TrimSpace(identity.DisplayName) != "" {
			return identity
		}
	}
	return deviceIdentity{DisplayName: fallback, NameSource: "address"}
}

func deviceIdentitySource(ctx context.Context, database *sql.DB, deviceID int64, nameSource string) string {
	if nameSource == "manual" {
		return "confirmed by you"
	}
	var kind string
	if err := database.QueryRowContext(ctx, `
		SELECT kind FROM device_identifiers WHERE device_id = ?
		ORDER BY CASE kind WHEN 'mac' THEN 0 WHEN 'local_dns' THEN 1 ELSE 2 END, last_seen_at DESC LIMIT 1`, deviceID).Scan(&kind); err == nil {
		switch kind {
		case "mac":
			return "local network identity"
		case "local_dns":
			return "Local DNS"
		}
	}
	if nameSource == "reverse_dns" {
		return "router name"
	}
	return "DNS activity"
}

func deviceAddressHistory(ctx context.Context, database *sql.DB, deviceID int64) []map[string]any {
	rows, err := database.QueryContext(ctx, `
		SELECT address, family, source, confidence,
		       strftime('%Y-%m-%dT%H:%M:%SZ', first_seen_at),
		       strftime('%Y-%m-%dT%H:%M:%SZ', last_seen_at)
		FROM device_addresses WHERE device_id = ? ORDER BY last_seen_at DESC, id DESC`, deviceID)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var address, family, source, confidence, firstSeen, lastSeen string
		if rows.Scan(&address, &family, &source, &confidence, &firstSeen, &lastSeen) == nil {
			items = append(items, map[string]any{
				"address": address, "family": family, "source": source, "confidence": confidence,
				"first_seen": firstSeen, "last_seen": lastSeen,
			})
		}
	}
	return items
}

func (s *Handler) device(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if path == "" {
		writeBadRequest(w, errors.New("client ip is required"))
		return
	}
	if strings.HasSuffix(path, "/alias") {
		rawClientIP := strings.TrimSuffix(path, "/alias")
		clientIP, err := url.PathUnescape(strings.Trim(rawClientIP, "/"))
		if err != nil || strings.TrimSpace(clientIP) == "" {
			writeBadRequest(w, errors.New("invalid client ip"))
			return
		}
		s.deviceAlias(w, r, clientIP)
		return
	}
	if strings.HasSuffix(path, "/protection") {
		rawClientIP := strings.TrimSuffix(path, "/protection")
		clientIP, err := url.PathUnescape(strings.Trim(rawClientIP, "/"))
		if err != nil || strings.TrimSpace(clientIP) == "" {
			writeBadRequest(w, errors.New("invalid client ip"))
			return
		}
		s.assignDeviceProtection(w, r, clientIP)
		return
	}
	if strings.HasSuffix(path, "/replay") {
		rawClientIP := strings.TrimSuffix(path, "/replay")
		clientIP, err := url.PathUnescape(strings.Trim(rawClientIP, "/"))
		if err != nil || strings.TrimSpace(clientIP) == "" {
			writeBadRequest(w, errors.New("invalid client ip"))
			return
		}
		s.deviceReplay(w, r, clientIP)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	clientIP, err := url.PathUnescape(path)
	if err != nil || strings.TrimSpace(clientIP) == "" {
		writeBadRequest(w, errors.New("invalid client ip"))
		return
	}
	deviceID, found, lookupErr := deviceidentity.DeviceIDForAddress(r.Context(), s.store, clientIP)
	if lookupErr != nil {
		writeBadRequest(w, lookupErr)
		return
	}
	if !found {
		writeBadRequest(w, errors.New("device was not found"))
		return
	}
	addresses, addressErr := deviceidentity.Addresses(r.Context(), s.store, deviceID)
	if addressErr != nil {
		writeError(w, addressErr)
		return
	}
	start := todayStart(r)
	var name, storedDeviceType, storedTypeSource string
	var location, notes sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT name, location, notes, device_type, type_source FROM devices WHERE id = ?`, deviceID).Scan(&name, &location, &notes, &storedDeviceType, &storedTypeSource)
	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ?`, deviceID, start)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND action = 'blocked'`, deviceID, start)
	var firstSeen, lastSeen sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE device_id = ?`, deviceID).Scan(&firstSeen, &lastSeen)
	discovered := s.discoverDeviceNames(r.Context(), addresses)
	identity := bestDeviceIdentity(discovered, addresses, clientIP)
	if strings.TrimSpace(name) != "" {
		identity.DisplayName = name
		identity.NameSource = "manual"
	}
	typeSource := "automatic"
	if storedTypeSource == "manual" && validManualDeviceType(storedDeviceType) {
		identity.DeviceType, identity.TypeConfidence, typeSource = storedDeviceType, "high", "manual"
	} else {
		identity.DeviceType, identity.TypeConfidence = inferDeviceTypeForDevice(r.Context(), s.store.DB, deviceID, clientIP, identity.DisplayName)
	}
	protectionID, protectionName, protectionIcon := protectionForClient(r.Context(), s.store.DB, clientIP)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_id":             deviceID,
		"client_ip":             clientIP,
		"addresses":             addresses,
		"address_history":       deviceAddressHistory(r.Context(), s.store.DB, deviceID),
		"identity_source":       deviceIdentitySource(r.Context(), s.store.DB, deviceID, identity.NameSource),
		"name":                  name,
		"display_name":          identity.DisplayName,
		"name_source":           identity.NameSource,
		"location":              nullableString(location),
		"notes":                 nullableString(notes),
		"device_type":           identity.DeviceType,
		"type_confidence":       identity.TypeConfidence,
		"type_source":           typeSource,
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"top_domains":           grouped(r.Context(), s.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 8`, deviceID, start),
		"first_seen":            nullableString(firstSeen),
		"last_seen":             nullableString(lastSeen),
		"profile":               protectionName,
		"protection":            protectionName,
		"protection_id":         protectionID,
		"protection_icon":       protectionIcon,
		"recent_activity":       recentQueriesFor(r.Context(), s.store.DB, `device_id = ?`, deviceID),
	})
}

func (s *Handler) deviceReplay(w http.ResponseWriter, r *http.Request, clientIP string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	deviceID, found, err := deviceidentity.DeviceIDForAddress(r.Context(), s.store, clientIP)
	if err != nil || !found {
		writeBadRequest(w, errors.New("device was not found"))
		return
	}
	from, to, bucket, rangeLabel, err := replayWindow(r.Context(), s.store.DB, deviceID, r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	fromText := from.UTC().Format(time.RFC3339)
	toText := to.UTC().Format(time.RFC3339)

	total := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?`, deviceID, fromText, toText)
	blocked := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ? AND action = 'blocked'`, deviceID, fromText, toText)
	uniqueDomains := scalarInt(r.Context(), s.store.DB, `SELECT COUNT(DISTINCT domain) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?`, deviceID, fromText, toText)

	bucketCount := int((to.Sub(from) + bucket - 1) / bucket)
	if bucketCount < 1 {
		bucketCount = 1
	}
	buckets := make([]map[string]any, bucketCount)
	for index := range buckets {
		buckets[index] = map[string]any{
			"timestamp": from.Add(time.Duration(index) * bucket).UTC().Format(time.RFC3339),
			"total":     0,
			"blocked":   0,
		}
	}
	rows, queryErr := s.store.DB.QueryContext(r.Context(), `
		SELECT
			CAST((strftime('%s', timestamp) - strftime('%s', ?)) / ? AS INTEGER) AS bucket_index,
			COUNT(*),
			COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries
		WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
		GROUP BY bucket_index
		ORDER BY bucket_index
	`, fromText, int(bucket.Seconds()), deviceID, fromText, toText)
	if queryErr == nil {
		defer rows.Close()
		for rows.Next() {
			var index, count, blockedCount int
			if scanErr := rows.Scan(&index, &count, &blockedCount); scanErr != nil {
				break
			}
			if index >= 0 && index < len(buckets) {
				buckets[index]["total"] = count
				buckets[index]["blocked"] = blockedCount
			}
		}
	}

	events, truncated := replayQueries(r.Context(), s.store.DB, deviceID, fromText, toText, 2500)
	durationMinutes := to.Sub(from).Minutes()
	queriesPerMinute := 0.0
	if durationMinutes > 0 {
		queriesPerMinute = float64(total) / durationMinutes
	}
	var firstSeen, lastSeen sql.NullString
	_ = s.store.DB.QueryRowContext(r.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?`, deviceID, fromText, toText).Scan(&firstSeen, &lastSeen)

	writeJSON(w, http.StatusOK, map[string]any{
		"client_ip":          clientIP,
		"range":              rangeLabel,
		"from":               fromText,
		"to":                 toText,
		"bucket_seconds":     int(bucket.Seconds()),
		"total_queries":      total,
		"blocked_queries":    blocked,
		"unique_domains":     uniqueDomains,
		"queries_per_minute": queriesPerMinute,
		"first_seen":         nullableString(firstSeen),
		"last_seen":          nullableString(lastSeen),
		"buckets":            buckets,
		"top_domains": grouped(r.Context(), s.store.DB, `
			SELECT domain, COUNT(*) FROM dns_queries
			WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
			GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 10
		`, deviceID, fromText, toText),
		"sources": grouped(r.Context(), s.store.DB, `
			SELECT source, COUNT(*) FROM dns_queries
			WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
			GROUP BY source ORDER BY COUNT(*) DESC, source
		`, deviceID, fromText, toText),
		"events":    events,
		"truncated": truncated,
	})
}

func replayWindow(ctx context.Context, database *sql.DB, deviceID int64, r *http.Request) (time.Time, time.Time, time.Duration, string, error) {
	now := time.Now().UTC()
	if fromRaw, toRaw := strings.TrimSpace(r.URL.Query().Get("from")), strings.TrimSpace(r.URL.Query().Get("to")); fromRaw != "" || toRaw != "" {
		from, fromErr := time.Parse(time.RFC3339, fromRaw)
		to, toErr := time.Parse(time.RFC3339, toRaw)
		if fromErr != nil || toErr != nil || !to.After(from) {
			return time.Time{}, time.Time{}, 0, "", errors.New("from and to must be valid RFC3339 timestamps with to after from")
		}
		return from.UTC(), to.UTC(), replayBucketSize(to.Sub(from)), "custom", nil
	}

	rangeLabel := strings.TrimSpace(r.URL.Query().Get("range"))
	if rangeLabel == "" {
		rangeLabel = "7d"
	}
	var from time.Time
	switch rangeLabel {
	case "1h":
		from = now.Add(-time.Hour)
	case "24h":
		from = now.Add(-24 * time.Hour)
	case "7d":
		from = now.Add(-7 * 24 * time.Hour)
	case "30d":
		from = now.Add(-30 * 24 * time.Hour)
	case "all":
		var firstSeen sql.NullString
		_ = database.QueryRowContext(ctx, `SELECT MIN(timestamp) FROM dns_queries WHERE device_id = ?`, deviceID).Scan(&firstSeen)
		if firstSeen.Valid {
			if parsed, parseErr := time.Parse(time.RFC3339, firstSeen.String); parseErr == nil {
				from = parsed.UTC()
			}
		}
		if from.IsZero() {
			from = now.Add(-7 * 24 * time.Hour)
		}
	default:
		return time.Time{}, time.Time{}, 0, "", errors.New("range must be one of 1h, 24h, 7d, 30d, or all")
	}
	return from, now, replayBucketSize(now.Sub(from)), rangeLabel, nil
}

func replayBucketSize(duration time.Duration) time.Duration {
	switch {
	case duration <= 2*time.Hour:
		return 5 * time.Minute
	case duration <= 2*24*time.Hour:
		return 30 * time.Minute
	case duration <= 8*24*time.Hour:
		return 3 * time.Hour
	case duration <= 32*24*time.Hour:
		return 12 * time.Hour
	default:
		hours := int(duration.Hours()/72) + 1
		if hours < 1 {
			hours = 1
		}
		return time.Duration(hours) * time.Hour
	}
}

func replayQueries(ctx context.Context, database *sql.DB, deviceID int64, from, to string, limit int) ([]map[string]any, bool) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata
		FROM dns_queries
		WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC, id ASC
		LIMIT ?
	`, deviceID, from, to, limit+1)
	if err != nil {
		return []map[string]any{}, false
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var timestamp, rowClientIP, domain, queryType, action, source, upstream, rcode, decisionReason, decisionMetadata string
		var latency sql.NullFloat64
		if scanErr := rows.Scan(&id, &timestamp, &rowClientIP, &domain, &queryType, &action, &source, &upstream, &latency, &rcode, &decisionReason, &decisionMetadata); scanErr != nil {
			break
		}
		items = append(items, map[string]any{
			"id":              id,
			"timestamp":       timestamp,
			"client_ip":       rowClientIP,
			"domain":          domain,
			"query_type":      queryType,
			"action":          action,
			"source":          source,
			"upstream":        upstream,
			"latency_ms":      nullableFloat(latency),
			"rcode":           rcode,
			"decision_reason": decisionReason,
			"decision":        metadataMap(decisionMetadata),
		})
	}
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	return items, truncated
}

func (s *Handler) deviceAlias(w http.ResponseWriter, r *http.Request, clientIP string) {
	if r.Method != http.MethodPut {
		methodNotAllowed(w)
		return
	}
	var input deviceAliasInput
	if !decode(w, r, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	deviceID, err := deviceidentity.ResolveAddress(r.Context(), s.store, clientIP, "manual")
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	var currentDeviceType, currentTypeSource string
	if err := s.store.DB.QueryRowContext(r.Context(), `SELECT device_type, type_source FROM devices WHERE id = ?`, deviceID).Scan(&currentDeviceType, &currentTypeSource); err != nil {
		writeError(w, err)
		return
	}
	if input.DeviceType != nil {
		currentDeviceType = strings.TrimSpace(*input.DeviceType)
		if currentDeviceType != "" && !validManualDeviceType(currentDeviceType) {
			writeBadRequest(w, errors.New("invalid device type"))
			return
		}
		if currentDeviceType == "" {
			currentTypeSource = ""
		} else {
			currentTypeSource = "manual"
		}
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `
		UPDATE devices SET name = ?, location = ?, notes = ?, device_type = ?, type_source = ?, confirmed = 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, name, nullableInput(input.Location), nullableInput(input.Notes), currentDeviceType, currentTypeSource, deviceID); err != nil {
		writeError(w, err)
		return
	}
	if _, err := s.store.DB.ExecContext(r.Context(), `
		INSERT INTO device_aliases(client_ip, name, location, notes, updated_at)
		VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(client_ip) DO UPDATE SET
			name = excluded.name,
			location = excluded.location,
			notes = excluded.notes,
			updated_at = CURRENT_TIMESTAMP
	`, clientIP, name, nullableInput(input.Location), nullableInput(input.Notes)); err != nil {
		writeError(w, err)
		return
	}
	s.recordEvent(r.Context(), eventInput{
		Type:        "device.alias_updated",
		Severity:    "info",
		Title:       "Device name updated",
		Description: fmt.Sprintf("%s is now known as %s.", clientIP, name),
		ClientIP:    clientIP,
		Metadata:    map[string]any{"name": name, "location": strings.TrimSpace(input.Location)},
		Source:      "devices",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var manualDeviceTypes = map[string]bool{
	"Computer":     true,
	"Phone":        true,
	"Tablet":       true,
	"TV":           true,
	"Game console": true,
	"Router":       true,
	"Server / NAS": true,
	"Smart home":   true,
	"Printer":      true,
	"Camera":       true,
	"Speaker":      true,
	"Vehicle":      true,
	"Other":        true,
}

func validManualDeviceType(deviceType string) bool {
	return manualDeviceTypes[strings.TrimSpace(deviceType)]
}

func displayDeviceName(ctx context.Context, database *sql.DB, clientIP string) string {
	var name string
	if err := database.QueryRowContext(ctx, `
		SELECT d.name FROM devices d JOIN device_addresses a ON a.device_id = d.id
		WHERE a.address = ?`, clientIP).Scan(&name); err == nil && strings.TrimSpace(name) != "" {
		return name
	}
	if err := database.QueryRowContext(ctx, `SELECT name FROM device_aliases WHERE client_ip = ?`, clientIP).Scan(&name); err == nil && strings.TrimSpace(name) != "" {
		return name
	}
	return clientIP
}
