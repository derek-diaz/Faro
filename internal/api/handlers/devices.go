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

	"github.com/derek/faro/internal/devicecatalog"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func (handler *Handler) devices(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	handler.deviceInventory(responseWriter, request)
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
	if nameSource == "unifi" {
		return "UniFi"
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
	defer closeRows(rows)
	items := make([]map[string]any, 0)
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

func (handler *Handler) device(responseWriter http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/devices/"), "/")
	if path == "" {
		writeBadRequest(responseWriter, errors.New("client ip is required"))
		return
	}
	if handler.routeDeviceSubresource(responseWriter, request, path) {
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	clientIP, err := unescapeDeviceIP(path)
	if err != nil {
		writeBadRequest(responseWriter, errors.New("invalid client ip"))
		return
	}
	handler.deviceDetails(responseWriter, request, clientIP)
}

func (handler *Handler) routeDeviceSubresource(responseWriter http.ResponseWriter, request *http.Request, path string) bool {
	if rawClientIP, ok := strings.CutSuffix(path, "/alias"); ok {
		handler.handleDeviceSubresource(responseWriter, request, rawClientIP, handler.deviceAlias)
		return true
	}
	if rawClientIP, ok := strings.CutSuffix(path, "/protection"); ok {
		handler.handleDeviceSubresource(responseWriter, request, rawClientIP, handler.assignDeviceProtection)
		return true
	}
	if rawClientIP, ok := strings.CutSuffix(path, "/pause"); ok {
		handler.handleDeviceSubresource(responseWriter, request, rawClientIP, handler.deviceDNSPause)
		return true
	}
	if rawClientIP, ok := strings.CutSuffix(path, "/replay"); ok {
		handler.handleDeviceSubresource(responseWriter, request, rawClientIP, handler.deviceReplay)
		return true
	}
	return false
}

func (handler *Handler) handleDeviceSubresource(responseWriter http.ResponseWriter, request *http.Request, rawClientIP string, action func(http.ResponseWriter, *http.Request, string)) {
	clientIP, err := unescapeDeviceIP(rawClientIP)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	action(responseWriter, request, clientIP)
}

func unescapeDeviceIP(rawClientIP string) (string, error) {
	clientIP, err := url.PathUnescape(strings.Trim(rawClientIP, "/"))
	if err != nil || strings.TrimSpace(clientIP) == "" {
		return "", errors.New("invalid client ip")
	}
	return clientIP, nil
}

func (handler *Handler) deviceDetails(responseWriter http.ResponseWriter, request *http.Request, clientIP string) {
	deviceID, found, lookupErr := deviceidentity.DeviceIDForAddress(request.Context(), handler.store, clientIP)
	if lookupErr != nil {
		writeBadRequest(responseWriter, lookupErr)
		return
	}
	if !found {
		writeBadRequest(responseWriter, errors.New("device was not found"))
		return
	}
	addresses, addressErr := deviceidentity.Addresses(request.Context(), handler.store, deviceID)
	if addressErr != nil {
		writeError(responseWriter, addressErr)
		return
	}
	start := todayStart(request)
	var name, storedDeviceType, storedTypeSource string
	var location, notes sql.NullString
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT name, location, notes, device_type, type_source FROM devices WHERE id = ?`, deviceID).Scan(&name, &location, &notes, &storedDeviceType, &storedTypeSource)
	total := scalarInt(request.Context(), handler.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ?`, deviceID, start)
	blocked := scalarInt(request.Context(), handler.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND action = 'blocked'`, deviceID, start)
	var firstSeen, lastSeen sql.NullString
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE device_id = ?`, deviceID).Scan(&firstSeen, &lastSeen)
	discovered := handler.discoverDeviceNames(request.Context(), addresses)
	identity := bestDeviceIdentity(discovered, addresses, clientIP)
	if strings.TrimSpace(name) != "" {
		identity.DisplayName = name
		identity.NameSource = "manual"
	}
	classification, classificationErr := devicecatalog.Classification(request.Context(), handler.store.DB, deviceID)
	if classificationErr != nil && !errors.Is(classificationErr, sql.ErrNoRows) {
		writeError(responseWriter, classificationErr)
		return
	}
	if errors.Is(classificationErr, sql.ErrNoRows) {
		classification = devicecatalog.Prediction{
			DeviceType:     "Unknown",
			Category:       "unknown",
			Icon:           "monitor",
			Confidence:     "unknown",
			CatalogVersion: handler.activeDeviceCatalog().Info().CatalogVersion,
			Evidence:       []devicecatalog.Evidence{},
		}
	}
	identity.DeviceType, identity.TypeConfidence = classification.DeviceType, classification.Confidence
	typeSource := "automatic"
	if storedTypeSource == "manual" && validManualDeviceType(storedDeviceType) {
		identity.DeviceType, identity.TypeConfidence, typeSource = storedDeviceType, "high", "manual"
	}
	protectionID, protectionName, protectionIcon := protectionForClient(request.Context(), handler.store.DB, clientIP)
	var dnsPausedUntil sql.NullString
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT paused_until FROM device_dns_pauses WHERE device_id = ?`, deviceID).Scan(&dnsPausedUntil)
	dnsPaused := false
	if dnsPausedUntil.Valid {
		if until, parseErr := time.Parse(time.RFC3339, dnsPausedUntil.String); parseErr == nil {
			dnsPaused = time.Now().Before(until)
		}
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"device_id":             deviceID,
		"client_ip":             clientIP,
		"addresses":             addresses,
		"address_history":       deviceAddressHistory(request.Context(), handler.store.DB, deviceID),
		"identity_source":       deviceIdentitySource(request.Context(), handler.store.DB, deviceID, identity.NameSource),
		"name":                  name,
		"display_name":          identity.DisplayName,
		"name_source":           identity.NameSource,
		"location":              nullableString(location),
		"notes":                 nullableString(notes),
		"device_type":           identity.DeviceType,
		"device_icon":           classification.Icon,
		"type_category":         classification.Category,
		"type_confidence":       identity.TypeConfidence,
		"type_source":           typeSource,
		"classification":        classificationResponse(classification, typeSource),
		"total_queries_today":   total,
		"blocked_queries_today": blocked,
		"block_percentage":      percentage(blocked, total),
		"top_domains":           grouped(request.Context(), handler.store.DB, `SELECT domain, COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 8`, deviceID, start),
		"first_seen":            nullableString(firstSeen),
		"last_seen":             nullableString(lastSeen),
		"profile":               protectionName,
		"protection":            protectionName,
		"protection_id":         protectionID,
		"protection_icon":       protectionIcon,
		"dns_paused":            dnsPaused,
		"dns_paused_until":      nullableString(dnsPausedUntil),
		"recent_activity":       recentQueriesFor(request.Context(), handler.store.DB, `device_id = ?`, deviceID),
	})
}

func (handler *Handler) deviceDNSPause(responseWriter http.ResponseWriter, request *http.Request, clientIP string) {
	if request.Method != http.MethodPut {
		methodNotAllowed(responseWriter)
		return
	}
	deviceID, found, err := deviceidentity.DeviceIDForAddress(request.Context(), handler.store, clientIP)
	if err != nil || !found {
		writeBadRequest(responseWriter, errors.New("device was not found"))
		return
	}
	var input struct {
		Until string `json:"until"`
	}
	if !decode(responseWriter, request, &input) {
		return
	}
	input.Until = strings.TrimSpace(input.Until)
	if input.Until != "" {
		until, parseErr := time.Parse(time.RFC3339, input.Until)
		if parseErr != nil || !until.After(time.Now()) {
			writeBadRequest(responseWriter, errors.New("pause end must be a future RFC3339 timestamp"))
			return
		}
		if until.After(time.Now().Add(7 * 24 * time.Hour)) {
			writeBadRequest(responseWriter, errors.New("a device can be paused for at most 7 days"))
			return
		}
		input.Until = until.UTC().Format(time.RFC3339)
	}
	handler.configMu.Lock()
	defer handler.configMu.Unlock()
	var previous sql.NullString
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT paused_until FROM device_dns_pauses WHERE device_id = ?`, deviceID).Scan(&previous)
	if input.Until == "" {
		_, err = handler.store.DB.ExecContext(request.Context(), `DELETE FROM device_dns_pauses WHERE device_id = ?`, deviceID)
	} else {
		_, err = handler.store.DB.ExecContext(request.Context(), `
			INSERT INTO device_dns_pauses(device_id, paused_until, updated_at) VALUES(?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT(device_id) DO UPDATE SET paused_until=excluded.paused_until, updated_at=CURRENT_TIMESTAMP`, deviceID, input.Until)
	}
	if err != nil {
		writeError(responseWriter, err)
		return
	}
	if err := handler.reloader.Apply(request.Context()); err != nil {
		rollbackCtx := context.WithoutCancel(request.Context())
		if previous.Valid {
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `INSERT INTO device_dns_pauses(device_id, paused_until) VALUES(?, ?) ON CONFLICT(device_id) DO UPDATE SET paused_until=excluded.paused_until, updated_at=CURRENT_TIMESTAMP`, deviceID, previous.String)
		} else {
			_, _ = handler.store.DB.ExecContext(rollbackCtx, `DELETE FROM device_dns_pauses WHERE device_id = ?`, deviceID)
		}
		_ = handler.reloader.Apply(rollbackCtx)
		writeError(responseWriter, fmt.Errorf("device pause was not changed because CoreDNS rejected the configuration: %w", err))
		return
	}
	name := clientIP
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT COALESCE(NULLIF(name, ''), ?) FROM devices WHERE id = ?`, clientIP, deviceID).Scan(&name)
	if input.Until == "" {
		handler.recordEvent(request.Context(), eventInput{Type: "device.dns_resumed", Severity: "success", Title: "Device internet access restored", Description: name + " can resolve internet names again.", ClientIP: clientIP, Metadata: map[string]any{"device_id": deviceID}, Source: "protection"})
	} else {
		handler.recordEvent(request.Context(), eventInput{Type: "device.dns_paused", Severity: "warning", Title: "Device internet access paused", Description: name + " cannot make most new website or app connections because DNS is paused.", ClientIP: clientIP, Metadata: map[string]any{"device_id": deviceID, "paused_until": input.Until}, Source: "protection"})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true, "paused_until": nullableTimeString(input.Until)})
}

func (handler *Handler) deviceReplay(responseWriter http.ResponseWriter, request *http.Request, clientIP string) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}

	deviceID, found, err := deviceidentity.DeviceIDForAddress(request.Context(), handler.store, clientIP)
	if err != nil || !found {
		writeBadRequest(responseWriter, errors.New("device was not found"))
		return
	}
	from, to, bucket, rangeLabel, err := replayWindow(request.Context(), handler.store.DB, deviceID, request)
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	fromText := from.UTC().Format(time.RFC3339)
	toText := to.UTC().Format(time.RFC3339)

	total := scalarInt(request.Context(), handler.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?`, deviceID, fromText, toText)
	blocked := scalarInt(request.Context(), handler.store.DB, `SELECT COUNT(*) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ? AND action = 'blocked'`, deviceID, fromText, toText)
	uniqueDomains := scalarInt(request.Context(), handler.store.DB, `SELECT COUNT(DISTINCT domain) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?`, deviceID, fromText, toText)

	buckets := newReplayBuckets(from, to, bucket)
	populateReplayBuckets(request.Context(), handler.store.DB, deviceID, fromText, toText, bucket, buckets)

	events, truncated := replayQueries(request.Context(), handler.store.DB, deviceID, fromText, toText, 2500)
	durationMinutes := to.Sub(from).Minutes()
	queriesPerMinute := 0.0
	if durationMinutes > 0 {
		queriesPerMinute = float64(total) / durationMinutes
	}
	var firstSeen, lastSeen sql.NullString
	_ = handler.store.DB.QueryRowContext(request.Context(), `SELECT MIN(timestamp), MAX(timestamp) FROM dns_queries WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?`, deviceID, fromText, toText).Scan(&firstSeen, &lastSeen)

	writeJSON(responseWriter, http.StatusOK, map[string]any{
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
		"top_domains": grouped(request.Context(), handler.store.DB, `
			SELECT domain, COUNT(*) FROM dns_queries
			WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
			GROUP BY domain ORDER BY COUNT(*) DESC, domain LIMIT 10
		`, deviceID, fromText, toText),
		"sources": grouped(request.Context(), handler.store.DB, `
			SELECT source, COUNT(*) FROM dns_queries
			WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
			GROUP BY source ORDER BY COUNT(*) DESC, source
		`, deviceID, fromText, toText),
		"events":    events,
		"truncated": truncated,
	})
}

func newReplayBuckets(from, to time.Time, bucket time.Duration) []map[string]any {
	bucketCount := max(int((to.Sub(from)+bucket-1)/bucket), 1)
	buckets := make([]map[string]any, bucketCount)
	for index := range buckets {
		buckets[index] = map[string]any{
			"timestamp": from.Add(time.Duration(index) * bucket).UTC().Format(time.RFC3339),
			"total":     0,
			"blocked":   0,
		}
	}
	return buckets
}

func populateReplayBuckets(ctx context.Context, database *sql.DB, deviceID int64, from, to string, bucket time.Duration, buckets []map[string]any) {
	rows, err := database.QueryContext(ctx, `
		SELECT
			CAST((strftime('%s', timestamp) - strftime('%s', ?)) / ? AS INTEGER) AS bucket_index,
			COUNT(*),
			COALESCE(SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END), 0)
		FROM dns_queries
		WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
		GROUP BY bucket_index
		ORDER BY bucket_index
	`, from, int(bucket.Seconds()), deviceID, from, to)
	if err != nil {
		return
	}
	defer closeRows(rows)
	for rows.Next() {
		var index, count, blockedCount int
		if err := rows.Scan(&index, &count, &blockedCount); err != nil {
			return
		}
		if index < 0 || index >= len(buckets) {
			continue
		}
		buckets[index]["total"] = count
		buckets[index]["blocked"] = blockedCount
	}
}

func replayWindow(ctx context.Context, database *sql.DB, deviceID int64, request *http.Request) (time.Time, time.Time, time.Duration, string, error) {
	now := time.Now().UTC()
	if fromRaw, toRaw := strings.TrimSpace(request.URL.Query().Get("from")), strings.TrimSpace(request.URL.Query().Get("to")); fromRaw != "" || toRaw != "" {
		from, fromErr := time.Parse(time.RFC3339, fromRaw)
		to, toErr := time.Parse(time.RFC3339, toRaw)
		if fromErr != nil || toErr != nil || !to.After(from) {
			return time.Time{}, time.Time{}, 0, "", errors.New("from and to must be valid RFC3339 timestamps with to after from")
		}
		return from.UTC(), to.UTC(), replayBucketSize(to.Sub(from)), "custom", nil
	}

	rangeLabel := strings.TrimSpace(request.URL.Query().Get("range"))
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
		hours := max(int(duration.Hours()/72)+1, 1)
		return time.Duration(hours) * time.Hour
	}
}

func replayQueries(ctx context.Context, database *sql.DB, deviceID int64, from, to string, limit int) ([]map[string]any, bool) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, timestamp, client_ip, domain, query_type, action, source, upstream, latency_ms, rcode, decision_reason, decision_metadata
		FROM dns_queries
		WHERE device_id = ? AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp, id
		LIMIT ?
	`, deviceID, from, to, limit+1)
	if err != nil {
		return []map[string]any{}, false
	}
	defer closeRows(rows)
	items := make([]map[string]any, 0)
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

func (handler *Handler) deviceAlias(responseWriter http.ResponseWriter, request *http.Request, clientIP string) {
	if request.Method != http.MethodPut {
		methodNotAllowed(responseWriter)
		return
	}
	var input deviceAliasInput
	if !decode(responseWriter, request, &input) {
		return
	}
	name := strings.TrimSpace(input.Name)
	deviceID, err := deviceidentity.ResolveAddress(request.Context(), handler.store, clientIP, "manual")
	if err != nil {
		writeBadRequest(responseWriter, err)
		return
	}
	var currentDeviceType, currentTypeSource string
	if err := handler.store.DB.QueryRowContext(request.Context(), `SELECT device_type, type_source FROM devices WHERE id = ?`, deviceID).Scan(&currentDeviceType, &currentTypeSource); err != nil {
		writeError(responseWriter, err)
		return
	}
	if input.DeviceType != nil {
		currentDeviceType = strings.TrimSpace(*input.DeviceType)
		if currentDeviceType != "" && !validManualDeviceType(currentDeviceType) {
			writeBadRequest(responseWriter, errors.New("invalid device type"))
			return
		}
		if currentDeviceType == "" {
			currentTypeSource = ""
		} else {
			currentTypeSource = "manual"
		}
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `
		UPDATE devices SET name = ?, location = ?, notes = ?, device_type = ?, type_source = ?, confirmed = 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`, name, nullableInput(input.Location), nullableInput(input.Notes), currentDeviceType, currentTypeSource, deviceID); err != nil {
		writeError(responseWriter, err)
		return
	}
	if _, err := handler.store.DB.ExecContext(request.Context(), `
		INSERT INTO device_aliases(client_ip, name, location, notes, updated_at)
		VALUES(?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(client_ip) DO UPDATE SET
			name = excluded.name,
			location = excluded.location,
			notes = excluded.notes,
			updated_at = CURRENT_TIMESTAMP
	`, clientIP, name, nullableInput(input.Location), nullableInput(input.Notes)); err != nil {
		writeError(responseWriter, err)
		return
	}
	// A changed friendly name can materially improve catalog matching. Mark the
	// cached prediction stale; the background classifier will rebuild it without
	// making this request wait for domain aggregation.
	if _, err := handler.store.DB.ExecContext(request.Context(), `DELETE FROM device_classifications WHERE device_id = ?`, deviceID); err != nil {
		writeError(responseWriter, err)
		return
	}
	handler.recordEvent(request.Context(), eventInput{
		Type:        "device.alias_updated",
		Severity:    "info",
		Title:       "Device name updated",
		Description: fmt.Sprintf("%s is now known as %s.", clientIP, name),
		ClientIP:    clientIP,
		Metadata:    map[string]any{"name": name, "location": strings.TrimSpace(input.Location)},
		Source:      "devices",
	})
	writeJSON(responseWriter, http.StatusOK, map[string]any{"ok": true})
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
