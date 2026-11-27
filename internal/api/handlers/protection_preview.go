package handlers

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

//go:embed protection_preview.sql
var protectionPreviewSQL string

const previewRequestLimit = 20000

type previewItem struct {
	Label   string `json:"label"`
	Address string `json:"address,omitempty"`
	Count   int    `json:"count"`
}

type previewGroup struct {
	Total int           `json:"total"`
	Items []previewItem `json:"items"`
}

type rulePreview struct {
	Requests     int          `json:"requests"`
	Truncated    bool         `json:"truncated"`
	NewlyBlocked previewGroup `json:"newly_blocked"`
	NewlyAllowed previewGroup `json:"newly_allowed"`
	StillBlocked previewGroup `json:"still_blocked"`
	Devices      previewGroup `json:"devices"`
}

func (handler *Handler) previewProtection(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var draft protectionInput
	if !decode(w, r, &draft) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	release, err := handler.readGate.acquire(ctx, "protection-preview")
	if err != nil {
		writeError(w, err)
		return
	}
	defer release()
	var exists bool
	if err := handler.store.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM protection_profiles WHERE id = ?)`, id).Scan(&exists); err != nil {
		writeError(w, err)
		return
	}
	if !exists {
		writeBadRequest(w, errors.New("protection does not exist"))
		return
	}
	draft, err = handler.normalizeProtectionRules(ctx, draft)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	result, err := compareProtectionRules(ctx, handler.store.DB, id, draft, previewRequestLimit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func compareProtectionRules(ctx context.Context, database *sql.DB, id int64, draft protectionInput, limit int) (rulePreview, error) {
	var result rulePreview
	payload, err := json.Marshal(draft)
	if err != nil {
		return result, err
	}
	rows, err := database.QueryContext(ctx, protectionPreviewSQL, id, string(payload), limit)
	if err != nil {
		return result, err
	}
	defer closeRows(rows)
	groups := map[string]*previewGroup{
		"newly_blocked": &result.NewlyBlocked, "newly_allowed": &result.NewlyAllowed,
		"still_blocked": &result.StillBlocked, "devices": &result.Devices,
	}
	for _, group := range groups {
		group.Items = []previewItem{}
	}
	for rows.Next() {
		var kind string
		var item previewItem
		var total int
		if err := rows.Scan(&kind, &item.Label, &item.Address, &item.Count, &total); err != nil {
			return result, err
		}
		if kind == "sample" {
			result.Requests = item.Count
			result.Truncated = total > 0
			continue
		}
		group := groups[kind]
		group.Total = total
		group.Items = append(group.Items, item)
	}
	return result, rows.Err()
}
