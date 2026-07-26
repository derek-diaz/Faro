package handlers

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type deviceInventoryOptions struct {
	page      int
	pageSize  int
	search    string
	sort      string
	direction string
	paged     bool
}

type inventoryBaseDevice struct {
	deviceID           int64
	clientIP           string
	name               string
	location           sql.NullString
	notes              sql.NullString
	storedDeviceType   string
	storedTypeSource   string
	total              int
	blocked            int
	lastSeen           sql.NullString
	classificationType string
	category           string
	icon               string
	confidence         string
	identifierKind     string
	protectionID       int64
	protectionName     string
	protectionIcon     string
}

type deviceInventorySummary struct {
	Observed           int    `json:"observed"`
	ActiveToday        int    `json:"active_today"`
	RequestsToday      int    `json:"requests_today"`
	BlockedToday       int    `json:"blocked_today"`
	MostActiveName     string `json:"most_active_name"`
	MostActiveRequests int    `json:"most_active_requests"`
}

func (s *Handler) deviceInventory(w http.ResponseWriter, r *http.Request) {
	options, err := parseDeviceInventoryOptions(r)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	revision, err := s.deviceInventoryRevision(r)
	if err != nil {
		writeError(w, err)
		return
	}
	etag := deviceInventoryETag(revision, options)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if options.paged && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	items, total, summary, err := s.loadDeviceInventory(r, options)
	if err != nil {
		writeError(w, err)
		return
	}
	if !options.paged {
		writeJSON(w, http.StatusOK, items)
		return
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + options.pageSize - 1) / options.pageSize
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":       items,
		"page":        options.page,
		"page_size":   options.pageSize,
		"total":       total,
		"total_pages": totalPages,
		"revision":    revision,
		"summary":     summary,
	})
}

func parseDeviceInventoryOptions(r *http.Request) (deviceInventoryOptions, error) {
	query := r.URL.Query()
	paged := query.Get("format") == "page" || query.Has("page") || query.Has("page_size") ||
		query.Has("search") || query.Has("sort") || query.Has("direction")
	options := deviceInventoryOptions{
		page:      1,
		pageSize:  50,
		search:    strings.TrimSpace(query.Get("search")),
		sort:      strings.TrimSpace(query.Get("sort")),
		direction: strings.ToLower(strings.TrimSpace(query.Get("direction"))),
		paged:     paged,
	}
	if !paged {
		options.pageSize = 10000
	}
	if raw := query.Get("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return options, fmt.Errorf("page must be a positive integer")
		}
		options.page = value
	}
	if raw := query.Get("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 200 {
			return options, fmt.Errorf("page_size must be between 1 and 200")
		}
		options.pageSize = value
	}
	if len(options.search) > 100 {
		return options, fmt.Errorf("search must be 100 characters or fewer")
	}
	switch options.sort {
	case "", "device":
		options.sort = "device"
	case "requests", "blocked", "last_seen", "protection":
	default:
		return options, fmt.Errorf("sort must be device, requests, blocked, last_seen, or protection")
	}
	switch options.direction {
	case "":
		if options.sort == "device" || options.sort == "protection" {
			options.direction = "asc"
		} else {
			options.direction = "desc"
		}
	case "asc", "desc":
	default:
		return options, fmt.Errorf("direction must be asc or desc")
	}
	return options, nil
}

func (s *Handler) deviceInventoryRevision(r *http.Request) (string, error) {
	var maxQueryID int64
	var devicesUpdated, addressesUpdated, namesUpdated, classificationsUpdated, membershipsUpdated, profilesUpdated, recordsUpdated string
	err := s.store.DB.QueryRowContext(r.Context(), `
		SELECT
			COALESCE((SELECT MAX(id) FROM dns_queries), 0),
			COALESCE((SELECT MAX(updated_at) FROM devices), ''),
			COALESCE((SELECT MAX(updated_at) FROM device_addresses), ''),
			COALESCE((SELECT MAX(updated_at) FROM device_names), ''),
			COALESCE((SELECT MAX(updated_at) FROM device_classifications), ''),
			COALESCE((SELECT MAX(updated_at) FROM device_protection_memberships), ''),
			COALESCE((SELECT MAX(updated_at) FROM protection_profiles), ''),
			COALESCE((SELECT MAX(updated_at) FROM dns_records), '')
	`).Scan(&maxQueryID, &devicesUpdated, &addressesUpdated, &namesUpdated, &classificationsUpdated, &membershipsUpdated, &profilesUpdated, &recordsUpdated)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%s:%s:%s:%s:%s:%s:%s", maxQueryID, devicesUpdated, addressesUpdated,
		namesUpdated, classificationsUpdated, membershipsUpdated, profilesUpdated, recordsUpdated), nil
}

func deviceInventoryETag(revision string, options deviceInventoryOptions) string {
	input := fmt.Sprintf("%s|%d|%d|%s|%s|%s", revision, options.page, options.pageSize, options.search, options.sort, options.direction)
	sum := sha256.Sum256([]byte(input))
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

func (s *Handler) loadDeviceInventory(r *http.Request, options deviceInventoryOptions) ([]map[string]any, int, deviceInventorySummary, error) {
	start := todayStart(r)
	searchPattern := "%" + strings.ToLower(options.search) + "%"
	sortExpression := map[string]string{
		"device":     "LOWER(COALESCE(NULLIF(TRIM(d.name), ''), NULLIF(TRIM(un.name), ''), NULLIF(TRIM(local.name), ''), la.address, ''))",
		"requests":   "COALESCE(day.total, 0)",
		"blocked":    "COALESCE(day.blocked, 0)",
		"last_seen":  "COALESCE(day.last_seen, d.last_seen_at, '')",
		"protection": "LOWER(COALESCE(member_profile.name, legacy_profile.name, default_profile.name, 'Home'))",
	}[options.sort]
	order := strings.ToUpper(options.direction)

	rows, err := s.store.DB.QueryContext(r.Context(), `
		WITH
		day AS (
			SELECT
				device_id,
				COUNT(*) AS total,
				SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END) AS blocked,
				MAX(timestamp) AS last_seen
			FROM dns_queries
			WHERE timestamp >= ? AND device_id IS NOT NULL
			GROUP BY device_id
		),
		latest_address AS (
			SELECT device_id, address
			FROM (
				SELECT device_id, address,
				       ROW_NUMBER() OVER (PARTITION BY device_id ORDER BY last_seen_at DESC, id DESC) AS position
				FROM device_addresses
			)
			WHERE position = 1
		),
		unifi_name AS (
			SELECT device_id, name FROM device_names WHERE source = 'unifi'
		),
		local_name AS (
			SELECT value, name
			FROM (
				SELECT value, hostname AS name,
				       ROW_NUMBER() OVER (PARTITION BY value ORDER BY updated_at DESC, id DESC) AS position
				FROM dns_records
				WHERE type IN ('A', 'AAAA')
			)
			WHERE position = 1
		)
		SELECT
			d.id,
			COALESCE(la.address, ''),
			d.name,
			d.location,
			d.notes,
			d.device_type,
			d.type_source,
			COALESCE(day.total, 0),
			COALESCE(day.blocked, 0),
			COALESCE(day.last_seen, strftime('%Y-%m-%dT%H:%M:%SZ', d.last_seen_at)),
			COALESCE(classification.predicted_type, 'Unknown'),
			COALESCE(classification.category, 'unknown'),
			COALESCE(classification.icon, 'monitor'),
			COALESCE(classification.confidence, 'unknown'),
			COALESCE((
				SELECT kind FROM device_identifiers
				WHERE device_id = d.id
				ORDER BY CASE kind WHEN 'mac' THEN 0 WHEN 'local_dns' THEN 1 ELSE 2 END, last_seen_at DESC
				LIMIT 1
			), ''),
			COALESCE(member_profile.id, legacy_profile.id, default_profile.id, 0),
			COALESCE(member_profile.name, legacy_profile.name, default_profile.name, 'Home'),
			COALESCE(member_profile.icon, legacy_profile.icon, default_profile.icon, 'house'),
			COUNT(*) OVER()
		FROM devices d
		LEFT JOIN latest_address la ON la.device_id = d.id
		LEFT JOIN day ON day.device_id = d.id
		LEFT JOIN unifi_name un ON un.device_id = d.id
		LEFT JOIN local_name local ON local.value = la.address
		LEFT JOIN device_classifications classification ON classification.device_id = d.id
		LEFT JOIN device_protection_memberships membership ON membership.device_id = d.id
		LEFT JOIN protection_profiles member_profile ON member_profile.id = membership.protection_id
		LEFT JOIN device_protection_assignments legacy ON legacy.client_ip = la.address
		LEFT JOIN protection_profiles legacy_profile ON legacy_profile.id = legacy.protection_id
		LEFT JOIN protection_profiles default_profile ON default_profile.is_default = 1
		WHERE ? = ''
		   OR LOWER(d.name) LIKE ?
		   OR LOWER(COALESCE(un.name, '')) LIKE ?
		   OR LOWER(COALESCE(local.name, '')) LIKE ?
		   OR LOWER(COALESCE(la.address, '')) LIKE ?
		   OR LOWER(COALESCE(NULLIF(d.device_type, ''), classification.predicted_type, '')) LIKE ?
		   OR LOWER(COALESCE(d.location, '')) LIKE ?
		   OR LOWER(COALESCE(member_profile.name, legacy_profile.name, default_profile.name, 'Home')) LIKE ?
		   OR EXISTS (
				SELECT 1 FROM device_addresses searched
				WHERE searched.device_id = d.id AND LOWER(searched.address) LIKE ?
		   )
		ORDER BY `+sortExpression+` `+order+`, d.id ASC
		LIMIT ? OFFSET ?
	`, start, options.search, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern, searchPattern,
		searchPattern, searchPattern, options.pageSize, (options.page-1)*options.pageSize)
	if err != nil {
		return nil, 0, deviceInventorySummary{}, err
	}
	defer rows.Close()

	baseDevices := make([]inventoryBaseDevice, 0, options.pageSize)
	total := 0
	for rows.Next() {
		var device inventoryBaseDevice
		if err := rows.Scan(
			&device.deviceID, &device.clientIP, &device.name, &device.location, &device.notes,
			&device.storedDeviceType, &device.storedTypeSource, &device.total, &device.blocked,
			&device.lastSeen, &device.classificationType, &device.category, &device.icon,
			&device.confidence, &device.identifierKind, &device.protectionID,
			&device.protectionName, &device.protectionIcon, &total,
		); err != nil {
			return nil, 0, deviceInventorySummary{}, err
		}
		baseDevices = append(baseDevices, device)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, deviceInventorySummary{}, err
	}
	if len(baseDevices) == 0 {
		total, err = s.inventoryMatchingCount(r, options.search)
		if err != nil {
			return nil, 0, deviceInventorySummary{}, err
		}
	}

	addressesByDevice, clientIPs, err := s.inventoryAddresses(r, baseDevices)
	if err != nil {
		return nil, 0, deviceInventorySummary{}, err
	}
	discoveredNames := s.discoverDeviceNames(r.Context(), clientIPs)
	items := make([]map[string]any, 0, len(baseDevices))
	for _, device := range baseDevices {
		addresses := addressesByDevice[device.deviceID]
		identity := bestDeviceIdentity(discoveredNames, addresses, device.clientIP)
		if strings.TrimSpace(device.name) != "" {
			identity.DisplayName = device.name
			identity.NameSource = "manual"
		}
		deviceType, confidence, typeSource := device.classificationType, device.confidence, "automatic"
		if device.storedTypeSource == "manual" && validManualDeviceType(device.storedDeviceType) {
			deviceType, confidence, typeSource = device.storedDeviceType, "high", "manual"
		}
		items = append(items, map[string]any{
			"device_id":             device.deviceID,
			"client_ip":             device.clientIP,
			"addresses":             addresses,
			"identity_source":       inventoryIdentitySource(identity.NameSource, device.identifierKind),
			"name":                  device.name,
			"display_name":          identity.DisplayName,
			"name_source":           identity.NameSource,
			"location":              nullableString(device.location),
			"notes":                 nullableString(device.notes),
			"device_type":           deviceType,
			"device_icon":           device.icon,
			"type_category":         device.category,
			"type_confidence":       confidence,
			"type_source":           typeSource,
			"total_queries_today":   device.total,
			"blocked_queries_today": device.blocked,
			"block_percentage":      percentage(device.blocked, device.total),
			"top_domains":           []map[string]any{},
			"last_seen":             nullableString(device.lastSeen),
			"profile":               device.protectionName,
			"protection":            device.protectionName,
			"protection_id":         device.protectionID,
			"protection_icon":       device.protectionIcon,
		})
	}
	summary, err := s.inventorySummary(r, start)
	if err != nil {
		return nil, 0, deviceInventorySummary{}, err
	}
	return items, total, summary, nil
}

func (s *Handler) inventoryMatchingCount(r *http.Request, search string) (int, error) {
	pattern := "%" + strings.ToLower(search) + "%"
	var total int
	err := s.store.DB.QueryRowContext(r.Context(), `
		WITH latest_address AS (
			SELECT device_id, address
			FROM (
				SELECT device_id, address,
				       ROW_NUMBER() OVER (PARTITION BY device_id ORDER BY last_seen_at DESC, id DESC) AS position
				FROM device_addresses
			)
			WHERE position = 1
		),
		local_name AS (
			SELECT value, name
			FROM (
				SELECT value, hostname AS name,
				       ROW_NUMBER() OVER (PARTITION BY value ORDER BY updated_at DESC, id DESC) AS position
				FROM dns_records
				WHERE type IN ('A', 'AAAA')
			)
			WHERE position = 1
		)
		SELECT COUNT(*)
		FROM devices d
		LEFT JOIN latest_address la ON la.device_id = d.id
		LEFT JOIN device_names un ON un.device_id = d.id AND un.source = 'unifi'
		LEFT JOIN local_name local ON local.value = la.address
		LEFT JOIN device_classifications classification ON classification.device_id = d.id
		LEFT JOIN device_protection_memberships membership ON membership.device_id = d.id
		LEFT JOIN protection_profiles member_profile ON member_profile.id = membership.protection_id
		LEFT JOIN device_protection_assignments legacy ON legacy.client_ip = la.address
		LEFT JOIN protection_profiles legacy_profile ON legacy_profile.id = legacy.protection_id
		LEFT JOIN protection_profiles default_profile ON default_profile.is_default = 1
		WHERE ? = ''
		   OR LOWER(d.name) LIKE ?
		   OR LOWER(COALESCE(un.name, '')) LIKE ?
		   OR LOWER(COALESCE(local.name, '')) LIKE ?
		   OR LOWER(COALESCE(la.address, '')) LIKE ?
		   OR LOWER(COALESCE(NULLIF(d.device_type, ''), classification.predicted_type, '')) LIKE ?
		   OR LOWER(COALESCE(d.location, '')) LIKE ?
		   OR LOWER(COALESCE(member_profile.name, legacy_profile.name, default_profile.name, 'Home')) LIKE ?
		   OR EXISTS (
				SELECT 1 FROM device_addresses searched
				WHERE searched.device_id = d.id AND LOWER(searched.address) LIKE ?
		   )
	`, search, pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern).Scan(&total)
	return total, err
}

func (s *Handler) inventoryAddresses(r *http.Request, devices []inventoryBaseDevice) (map[int64][]string, []string, error) {
	result := make(map[int64][]string, len(devices))
	if len(devices) == 0 {
		return result, []string{}, nil
	}
	arguments := make([]any, 0, len(devices))
	placeholders := make([]string, 0, len(devices))
	for _, device := range devices {
		arguments = append(arguments, device.deviceID)
		placeholders = append(placeholders, "?")
	}
	rows, err := s.store.DB.QueryContext(r.Context(), `
		SELECT device_id, address
		FROM device_addresses
		WHERE device_id IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY device_id, last_seen_at DESC, id DESC
	`, arguments...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	clientIPs := make([]string, 0, len(devices)*2)
	for rows.Next() {
		var deviceID int64
		var address string
		if err := rows.Scan(&deviceID, &address); err != nil {
			return nil, nil, err
		}
		result[deviceID] = append(result[deviceID], address)
		clientIPs = append(clientIPs, address)
	}
	return result, clientIPs, rows.Err()
}

func (s *Handler) inventorySummary(r *http.Request, start string) (deviceInventorySummary, error) {
	var summary deviceInventorySummary
	err := s.store.DB.QueryRowContext(r.Context(), `
		WITH day AS (
			SELECT device_id, COUNT(*) AS total,
			       SUM(CASE WHEN action = 'blocked' THEN 1 ELSE 0 END) AS blocked
			FROM dns_queries
			WHERE timestamp >= ? AND device_id IS NOT NULL
			GROUP BY device_id
		),
		most_active AS (
			SELECT device_id, total FROM day ORDER BY total DESC, device_id LIMIT 1
		)
		SELECT
			(SELECT COUNT(*) FROM devices),
			(SELECT COUNT(*) FROM day WHERE total > 0),
			COALESCE((SELECT SUM(total) FROM day), 0),
			COALESCE((SELECT SUM(blocked) FROM day), 0),
			COALESCE((
				SELECT COALESCE(NULLIF(TRIM(d.name), ''), NULLIF(TRIM(n.name), ''), (
					SELECT address FROM device_addresses
					WHERE device_id = d.id ORDER BY last_seen_at DESC, id DESC LIMIT 1
				), 'None')
				FROM most_active active
				JOIN devices d ON d.id = active.device_id
				LEFT JOIN device_names n ON n.device_id = d.id AND n.source = 'unifi'
			), 'None'),
			COALESCE((SELECT total FROM most_active), 0)
	`, start).Scan(
		&summary.Observed, &summary.ActiveToday, &summary.RequestsToday, &summary.BlockedToday,
		&summary.MostActiveName, &summary.MostActiveRequests,
	)
	return summary, err
}

func inventoryIdentitySource(nameSource, identifierKind string) string {
	switch nameSource {
	case "manual":
		return "confirmed by you"
	case "unifi":
		return "UniFi"
	case "reverse_dns":
		return "router name"
	}
	switch identifierKind {
	case "mac":
		return "local network identity"
	case "local_dns":
		return "Local DNS"
	default:
		return "DNS activity"
	}
}
