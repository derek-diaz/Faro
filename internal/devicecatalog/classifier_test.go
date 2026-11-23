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

func TestClassifierHonorsCooldownAndRefreshesChangedDevices(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "faro.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`INSERT INTO devices(id, name) VALUES(1, 'Idle device'), (2, 'Active device')`); err != nil {
		t.Fatal(err)
	}
	classifier := NewClassifier(store, NewManager(""))
	check := func(want int) {
		t.Helper()
		count, err := classifier.ClassifyPending(context.Background(), 10)
		if err != nil || count != want {
			t.Fatalf("processed = %d, want %d (%v)", count, want, err)
		}
	}
	check(2)
	if _, err := store.DB.Exec(`INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source)
		VALUES(CURRENT_TIMESTAMP, '192.0.2.2', 2, 'mtalk.google.com', 'A', 'allowed', 'upstream')`); err != nil {
		t.Fatal(err)
	}
	check(0)
	if _, err := store.DB.Exec(`UPDATE device_classifications SET evaluated_at = datetime('now', '-1 hour')`); err != nil {
		t.Fatal(err)
	}
	check(1)
	check(0)
	if _, err := store.DB.Exec(`UPDATE device_classifications SET catalog_version = 'outdated' WHERE device_id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB.Exec(`INSERT INTO devices(id, name) VALUES(3, 'New device')`); err != nil {
		t.Fatal(err)
	}
	if count, err := classifier.ClassifyPending(context.Background(), 1); err != nil || count != 1 {
		t.Fatalf("limited batch: %d, %v", count, err)
	}
	if _, err := Classification(context.Background(), store.DB, 3); err != nil {
		t.Fatalf("new device should be classified before outdated devices: %v", err)
	}
	check(1)
	check(0)
}
