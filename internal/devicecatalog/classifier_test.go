package devicecatalog

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/derek/faro/internal/db"
	deviceidentity "github.com/derek/faro/internal/devices"
)

func TestClassifierProcessesPendingDevicesOutsideRequestPath(t *testing.T) {
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

	classifier := NewClassifier(store, NewManager(""))
	processed, err := classifier.ClassifyPending(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	prediction, err := Classification(context.Background(), store.DB, deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if prediction.DeviceType != "Android Device" || prediction.Confidence != "medium" {
		t.Fatalf("prediction = %#v", prediction)
	}
	var classifiedQueryID int64
	if err := store.DB.QueryRow(`SELECT classified_query_id FROM device_classifications WHERE device_id = ?`, deviceID).Scan(&classifiedQueryID); err != nil {
		t.Fatal(err)
	}
	if classifiedQueryID < 1 {
		t.Fatalf("classified_query_id = %d", classifiedQueryID)
	}
}
