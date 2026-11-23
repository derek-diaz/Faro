package devicecatalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/derek/faro/internal/db"
)

// Measures the common idle poll: household history is large, but every device is in cooldown.
func BenchmarkClassifierCooldown(b *testing.B) {
	store, err := db.Open(filepath.Join(b.TempDir(), "faro.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB.Exec(`
		WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<20)
		INSERT INTO devices(id, name) SELECT i, 'Device ' || i FROM n;
		WITH RECURSIVE n(i) AS (VALUES(1) UNION ALL SELECT i+1 FROM n WHERE i<100000)
		INSERT INTO dns_queries(timestamp, client_ip, device_id, domain, query_type, action, source)
		SELECT CURRENT_TIMESTAMP, '192.0.2.' || (i%20+1), i%20+1, 'site-' || (i%100) || '.example', 'A', 'allowed', 'upstream' FROM n;
		ANALYZE;`); err != nil {
		b.Fatal(err)
	}
	classifier := NewClassifier(store, NewManager(""))
	if count, err := classifier.ClassifyPending(context.Background(), 50); err != nil || count != 20 {
		b.Fatalf("initial classification: %d, %v", count, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if count, err := classifier.ClassifyPending(context.Background(), 50); err != nil || count != 0 {
			b.Fatalf("cooldown poll: %d, %v", count, err)
		}
	}
}
