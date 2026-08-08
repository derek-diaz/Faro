package handlers

import (
	"net/http"

	"github.com/derek/faro/internal/devicecatalog"
)

var bundledDeviceCatalog = devicecatalog.NewManager("")

func (handler *Handler) activeDeviceCatalog() *devicecatalog.Manager {
	if handler.deviceCatalog != nil {
		return handler.deviceCatalog
	}
	return bundledDeviceCatalog
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

func (handler *Handler) deviceCatalogInfo(responseWriter http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(responseWriter)
		return
	}
	writeJSON(responseWriter, http.StatusOK, handler.activeDeviceCatalog().Info())
}
