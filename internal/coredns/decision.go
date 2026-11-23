package coredns

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/protectiontime"
)

type RuleMatch struct {
	Kind string `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type LocalRecordMatch struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DomainDecision struct {
	Action       string            `json:"action"`
	Reason       string            `json:"reason"`
	Protection   *RuleMatch        `json:"protection,omitempty"`
	Allowlist    *RuleMatch        `json:"allowlist,omitempty"`
	ManualBlock  *RuleMatch        `json:"manual_block,omitempty"`
	Blocklists   []RuleMatch       `json:"blocklists,omitempty"`
	LocalRecord  *LocalRecordMatch `json:"local_record,omitempty"`
	Confidence   string            `json:"confidence,omitempty"`
	CapturedAt   string            `json:"captured_at,omitempty"`
	Upstream     string            `json:"upstream,omitempty"`
	ResponseCode string            `json:"response_code,omitempty"`
}

func ExplainDomain(ctx context.Context, store *db.Store, domain string) DomainDecision {
	return ExplainDomainForClient(ctx, store, domain, "")
}

func ExplainDomainForClient(ctx context.Context, store *db.Store, domain, clientIP string) DomainDecision {
	return ExplainQueryForClient(ctx, store.DB, domain, clientIP, "")
}

type decisionReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ExplainQueryForClient restricts local-record matches to the question's type.
// An empty query type retains the domain overview used outside query history.
func ExplainQueryForClient(ctx context.Context, database decisionReader, domain, clientIP, queryType string) DomainDecision {
	normalized, err := db.NormalizeDomain(domain)
	if err != nil {
		return DomainDecision{Action: "allowed"}
	}
	decision := DomainDecision{Action: "allowed"}
	if clientIP != "" {
		var pausedUntil string
		if database.QueryRowContext(ctx, `
			SELECT p.paused_until FROM device_dns_pauses p
			JOIN device_addresses a ON a.device_id = p.device_id
			WHERE a.address = ? LIMIT 1`, clientIP).Scan(&pausedUntil) == nil && protectiontime.PausedAt(pausedUntil, time.Now()) {
			decision.Action = "blocked"
			decision.Reason = "DNS access is temporarily paused for this device."
			return decision
		}
	}
	var protectionID int64
	var protectionName string
	var pausedUntil, scheduleDays, scheduleStart, scheduleEnd, scheduleTimezone string
	var scheduleEnabled bool
	var allowID, manualID int64
	var localID int64
	var localType, localValue string
	err = database.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.paused_until, p.schedule_enabled, p.schedule_days, p.schedule_start, p.schedule_end, p.schedule_timezone,
		       COALESCE((SELECT id FROM protection_allow_entries WHERE protection_id = p.id AND domain = ? LIMIT 1), 0),
		       COALESCE((SELECT id FROM protection_block_entries WHERE protection_id = p.id AND domain = ? LIMIT 1), 0),
		       COALESCE(local.id, 0), COALESCE(local.type, ''), COALESCE(local.value, '')
		FROM protection_profiles p
		LEFT JOIN (SELECT id, type, value FROM dns_records WHERE hostname = ? AND (? = '' OR type = ?) ORDER BY id LIMIT 1) local ON 1 = 1
		LEFT JOIN device_addresses da ON da.address = ?
		LEFT JOIN device_protection_memberships m ON m.protection_id = p.id AND m.device_id = da.device_id
		LEFT JOIN device_protection_assignments legacy ON legacy.protection_id = p.id AND legacy.client_ip = ?
		WHERE m.device_id IS NOT NULL OR legacy.client_ip IS NOT NULL OR p.is_default = 1
		ORDER BY CASE WHEN m.device_id IS NOT NULL THEN 0 WHEN legacy.client_ip IS NOT NULL THEN 1 ELSE 2 END
		LIMIT 1
	`, normalized, normalized, normalized, queryType, queryType, clientIP, clientIP).Scan(&protectionID, &protectionName, &pausedUntil, &scheduleEnabled, &scheduleDays, &scheduleStart, &scheduleEnd, &scheduleTimezone, &allowID, &manualID, &localID, &localType, &localValue)
	if err != nil {
		var local LocalRecordMatch
		if lookupErr := database.QueryRowContext(ctx, `SELECT id, type, value FROM dns_records WHERE hostname = ? AND (? = '' OR type = ?) ORDER BY id LIMIT 1`, normalized, queryType, queryType).Scan(&local.ID, &local.Type, &local.Value); lookupErr == nil {
			decision.LocalRecord = &local
		}
		return decision
	}
	decision.Protection = &RuleMatch{Kind: "protection", ID: protectionID, Name: protectionName}
	var trialID int64
	if database.QueryRowContext(ctx, `SELECT id FROM troubleshooting_exceptions WHERE protection_id = ? AND domain = ? AND julianday(expires_at) > julianday('now') LIMIT 1`, protectionID, normalized).Scan(&trialID) == nil {
		decision.Allowlist = &RuleMatch{Kind: "allowlist", ID: trialID, Name: protectionName + " temporary troubleshooting exception"}
	}
	if localID != 0 {
		decision.LocalRecord = &LocalRecordMatch{ID: localID, Type: localType, Value: localValue}
	}
	if allowID != 0 {
		decision.Allowlist = &RuleMatch{Kind: "allowlist", ID: allowID, Name: protectionName + " exception"}
	}
	if manualID != 0 {
		decision.ManualBlock = &RuleMatch{Kind: "manual_block", ID: manualID, Name: protectionName + " custom block"}
	}

	rows, err := database.QueryContext(ctx, `
		SELECT b.id, b.name
		FROM blocklist_entries e
		JOIN blocklists b ON b.id = e.blocklist_id
		JOIN protection_blocklists p ON p.blocklist_id = b.id
		WHERE b.enabled = 1 AND p.protection_id = ? AND e.domain = ?
		ORDER BY b.name
	`, protectionID, normalized)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var match RuleMatch
			match.Kind = "blocklist"
			if rows.Scan(&match.ID, &match.Name) == nil {
				decision.Blocklists = append(decision.Blocklists, match)
			}
		}
	}

	filteringActive := !protectiontime.PausedAt(pausedUntil, time.Now()) && protectiontime.ActiveAt(protectiontime.Schedule{Enabled: scheduleEnabled, Days: scheduleDays, Start: scheduleStart, End: scheduleEnd, Timezone: scheduleTimezone}, time.Now())
	switch {
	case !filteringActive:
		decision.Reason = "Filtering is currently bypassed by " + protectionName + " time controls."
	case decision.Allowlist != nil:
		decision.Reason = "An exception in " + protectionName + " bypassed filtering."
	case decision.ManualBlock != nil:
		decision.Action = "blocked"
		decision.Reason = "Matched a custom block in " + protectionName + "."
	case len(decision.Blocklists) == 1:
		decision.Action = "blocked"
		decision.Reason = "Matched the " + decision.Blocklists[0].Name + " blocklist in " + protectionName + "."
	case len(decision.Blocklists) > 1:
		decision.Action = "blocked"
		decision.Reason = fmt.Sprintf("Matched %d blocklists in %s.", len(decision.Blocklists), protectionName)
	case decision.LocalRecord != nil:
		decision.Reason = "Matched a Faro Local DNS record."
	}
	return decision
}
