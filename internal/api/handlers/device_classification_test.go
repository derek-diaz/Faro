package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
	"github.com/derek/faro/internal/devicecatalog"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func TestDeviceEndpointIncludesCatalogEvidence(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	deviceID, err := deviceidentity.ResolveAddress(context.Background(), store, "192.168.1.23", "dns")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, domain := range []string{"connectivitycheck.gstatic.com", "mtalk.google.com"} {
		if _, err := store.DB.Exec(`
			INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source)
			VALUES(?, '192.168.1.23', ?, ?, 'A', 'allowed', 'upstream')
		`, now, deviceID, domain); err != nil {
			t.Fatal(err)
		}
	}

	classifier := devicecatalog.NewClassifier(store, devicecatalog.NewManager(""))
	processed, err := classifier.ClassifyPending(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed devices = %d, want 1", processed)
	}
	prediction, err := devicecatalog.Classification(context.Background(), store.DB, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if prediction.DeviceType != "Android Device" || prediction.CatalogVersion == "" || len(prediction.Evidence) != 2 {
		t.Fatalf("prediction = %#v", prediction)
	}

	var storedType, catalogVersion, evidenceJSON string
	if err := store.DB.QueryRow(`
		SELECT predicted_type, catalog_version, evidence_json
		FROM device_classifications WHERE device_id = ?
	`, deviceID).Scan(&storedType, &catalogVersion, &evidenceJSON); err != nil {
		t.Fatal(err)
	}
	var evidence []map[string]any
	if err := json.Unmarshal([]byte(evidenceJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	if storedType != "Android Device" || catalogVersion != prediction.CatalogVersion || len(evidence) != 2 {
		t.Fatalf("stored classification = type %q catalog %q evidence %#v", storedType, catalogVersion, evidence)
	}

	handler := &Handler{store: store}
	response := httptest.NewRecorder()
	handler.device(response, httptest.NewRequest(http.MethodGet, "/api/devices/192.168.1.23", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("device response status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		DeviceType     string `json:"device_type"`
		TypeConfidence string `json:"type_confidence"`
		Classification struct {
			PredictedType  string           `json:"predicted_type"`
			CatalogVersion string           `json:"catalog_version"`
			Evidence       []map[string]any `json:"evidence"`
		} `json:"classification"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.DeviceType != "Android Device" || payload.TypeConfidence != "medium" ||
		payload.Classification.PredictedType != "Android Device" ||
		payload.Classification.CatalogVersion != prediction.CatalogVersion ||
		len(payload.Classification.Evidence) != 2 {
		t.Fatalf("device classification payload = %#v", payload)
	}
}
