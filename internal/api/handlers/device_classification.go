package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/derek/faro/internal/devicecatalog"
)

var bundledDeviceCatalog = devicecatalog.NewManager("")

func (s *Handler) activeDeviceCatalog() *devicecatalog.Manager {
	if s.deviceCatalog != nil {
		return s.deviceCatalog
	}
	return bundledDeviceCatalog
}

func (s *Handler) classifyDevice(ctx context.Context, deviceID int64, primaryAddress, name string) (devicecatalog.Prediction, error) {
	domains := topLabels(ctx, s.store.DB, `
		SELECT domain, COUNT(*) FROM dns_queries
		WHERE device_id = ?
		GROUP BY domain
		ORDER BY COUNT(*) DESC, domain
		LIMIT 80
	`, deviceID)
	prediction := s.activeDeviceCatalog().Predict(name, primaryAddress, domains)
	evidence, err := json.Marshal(prediction.Evidence)
	if err != nil {
		return devicecatalog.Prediction{}, err
	}
	if _, err := s.store.DB.ExecContext(ctx, `
		INSERT INTO device_classifications(
			device_id, catalog_version, definition_id, predicted_type, category, icon,
			confidence, score, signal_hash, evidence_json, evaluated_at, updated_at
		)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
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
			updated_at = CURRENT_TIMESTAMP
		WHERE device_classifications.catalog_version <> excluded.catalog_version
		   OR device_classifications.signal_hash <> excluded.signal_hash
	`, deviceID, prediction.CatalogVersion, prediction.DefinitionID, prediction.DeviceType,
		prediction.Category, prediction.Icon, prediction.Confidence, prediction.Score,
		prediction.SignalHash, string(evidence), prediction.EvaluatedAt); err != nil {
		return devicecatalog.Prediction{}, err
	}

	var storedEvidence string
	if err := s.store.DB.QueryRowContext(ctx, `
		SELECT catalog_version, definition_id, predicted_type, category, icon,
		       confidence, score, signal_hash, evidence_json, evaluated_at
		FROM device_classifications WHERE device_id = ?
	`, deviceID).Scan(
		&prediction.CatalogVersion, &prediction.DefinitionID, &prediction.DeviceType,
		&prediction.Category, &prediction.Icon, &prediction.Confidence, &prediction.Score,
		&prediction.SignalHash, &storedEvidence, &prediction.EvaluatedAt,
	); err != nil {
		return devicecatalog.Prediction{}, err
	}
	prediction.Evidence = []devicecatalog.Evidence{}
	if err := json.Unmarshal([]byte(storedEvidence), &prediction.Evidence); err != nil {
		return devicecatalog.Prediction{}, err
	}
	return prediction, nil
}

func classificationResponse(prediction devicecatalog.Prediction, activeSource string) map[string]any {
	evidence := prediction.Evidence
	if len(evidence) > 4 {
		evidence = evidence[:4]
	}
	return map[string]any{
		"source":          activeSource,
		"definition_id":   prediction.DefinitionID,
		"predicted_type":  prediction.DeviceType,
		"category":        prediction.Category,
		"icon":            prediction.Icon,
		"confidence":      prediction.Confidence,
		"score":           prediction.Score,
		"catalog_version": prediction.CatalogVersion,
		"evidence":        evidence,
		"evaluated_at":    prediction.EvaluatedAt,
	}
}

func (s *Handler) deviceCatalogInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.activeDeviceCatalog().Info())
}
