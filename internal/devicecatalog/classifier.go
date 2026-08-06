package devicecatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/derek/faro/internal/db"
)

const (
	classificationInterval = 30 * time.Second
	classificationCooldown = 10 * time.Minute
	classificationBatch    = 50
)

// Classifier keeps device classifications current outside request handlers.
// DNS traffic can be very busy, so an active device is evaluated at most once
// per cooldown unless its catalog version or manually maintained metadata
// changes.
type Classifier struct {
	store   *db.Store
	catalog *Manager
}

func NewClassifier(store *db.Store, catalog *Manager) *Classifier {
	return &Classifier{store: store, catalog: catalog}
}

func (c *Classifier) Catalog() *Manager {
	return c.catalog
}

func (c *Classifier) Run(ctx context.Context) {
	c.classifyAvailable(ctx)
	ticker := time.NewTicker(classificationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.classifyAvailable(ctx)
		}
	}
}

func (c *Classifier) classifyAvailable(ctx context.Context) {
	for range 4 {
		count, err := c.ClassifyPending(ctx, classificationBatch)
		if err != nil || count < classificationBatch {
			return
		}
	}
}

// ClassifyPending evaluates a bounded batch and returns the number processed.
// It is exported so startup checks and tests can run a batch synchronously.
func (c *Classifier) ClassifyPending(ctx context.Context, limit int) (processed int, err error) {
	if limit < 1 {
		return 0, nil
	}
	info := c.catalog.Info()
	rows, err := c.store.DB.QueryContext(ctx, `
		WITH activity AS (
			SELECT device_id, MAX(id) AS query_id
			FROM dns_queries
			WHERE device_id IS NOT NULL
			GROUP BY device_id
		)
		SELECT
			d.id,
			COALESCE((
				SELECT address FROM device_addresses
				WHERE device_id = d.id
				ORDER BY last_seen_at DESC, id DESC LIMIT 1
			), ''),
			COALESCE(NULLIF(TRIM(d.name), ''), (
				SELECT name FROM device_names
				WHERE device_id = d.id AND TRIM(name) <> ''
				ORDER BY CASE source WHEN 'unifi' THEN 0 ELSE 1 END, last_seen_at DESC
				LIMIT 1
			), ''),
			COALESCE(activity.query_id, 0)
		FROM devices d
		LEFT JOIN activity ON activity.device_id = d.id
		LEFT JOIN device_classifications c ON c.device_id = d.id
		WHERE c.device_id IS NULL
		   OR c.catalog_version <> ?
		   OR (
				c.classified_query_id < COALESCE(activity.query_id, 0)
				AND julianday(c.evaluated_at) < julianday('now', ?)
		   )
		ORDER BY
			CASE WHEN c.device_id IS NULL THEN 0 WHEN c.catalog_version <> ? THEN 1 ELSE 2 END,
			COALESCE(activity.query_id, 0) DESC,
			d.id
		LIMIT ?
	`, info.CatalogVersion, fmt.Sprintf("-%d seconds", int(classificationCooldown.Seconds())), info.CatalogVersion, limit)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	type candidate struct {
		deviceID int64
		address  string
		name     string
		queryID  int64
	}
	candidates := make([]candidate, 0, limit)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.deviceID, &item.address, &item.name, &item.queryID); err != nil {
			return 0, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, item := range candidates {
		if err := c.classify(ctx, item.deviceID, item.address, item.name, item.queryID); err != nil {
			return 0, err
		}
	}
	return len(candidates), nil
}

func (c *Classifier) classify(ctx context.Context, deviceID int64, address, name string, queryID int64) error {
	rows, err := c.store.DB.QueryContext(ctx, `
		SELECT domain
		FROM dns_queries
		WHERE device_id = ?
		GROUP BY domain
		ORDER BY COUNT(*) DESC, domain
		LIMIT 80
	`, deviceID)
	if err != nil {
		return err
	}
	domains := make([]string, 0, 80)
	for rows.Next() {
		var domain string
		if err := rows.Scan(&domain); err != nil {
			_ = rows.Close()
			return err
		}
		domains = append(domains, domain)
	}
	if err := rows.Close(); err != nil {
		return err
	}

	prediction := c.catalog.Predict(name, address, domains)
	evidence, err := json.Marshal(prediction.Evidence)
	if err != nil {
		return err
	}
	_, err = c.store.DB.ExecContext(ctx, `
		INSERT INTO device_classifications(
			device_id, catalog_version, definition_id, predicted_type, category, icon,
			confidence, score, signal_hash, evidence_json, evaluated_at, classified_query_id, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(device_id) DO UPDATE SET
			catalog_version = excluded.catalog_version,
			definition_id = excluded.definition_id,
			predicted_type = excluded.predicted_type,
			category = excluded.category,
			icon = excluded.icon,
			confidence = excluded.confidence,
			score = excluded.score,
			signal_hash = excluded.signal_hash,
			evidence_json = excluded.evidence_json,
			evaluated_at = excluded.evaluated_at,
			classified_query_id = excluded.classified_query_id,
			updated_at = CURRENT_TIMESTAMP
	`, deviceID, prediction.CatalogVersion, prediction.DefinitionID, prediction.DeviceType,
		prediction.Category, prediction.Icon, prediction.Confidence, prediction.Score,
		prediction.SignalHash, string(evidence), prediction.EvaluatedAt, queryID)
	return err
}

func Classification(ctx context.Context, database *sql.DB, deviceID int64) (Prediction, error) {
	var prediction Prediction
	var evidenceJSON string
	err := database.QueryRowContext(ctx, `
		SELECT catalog_version, definition_id, predicted_type, category, icon,
		       confidence, score, signal_hash, evidence_json, evaluated_at
		FROM device_classifications
		WHERE device_id = ?
	`, deviceID).Scan(
		&prediction.CatalogVersion, &prediction.DefinitionID, &prediction.DeviceType,
		&prediction.Category, &prediction.Icon, &prediction.Confidence, &prediction.Score,
		&prediction.SignalHash, &evidenceJSON, &prediction.EvaluatedAt,
	)
	if err != nil {
		return Prediction{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &prediction.Evidence); err != nil {
		return Prediction{}, err
	}
	return prediction, nil
}
