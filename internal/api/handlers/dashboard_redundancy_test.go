package handlers

import (
	"context"
	"testing"
)

func TestDashboardWarnsUntilAllReplicasCatchUp(t *testing.T) {
	h, _, _ := troubleshootingFixture(t)
	database := h.store.DB
	_, err := database.Exec(`UPDATE redundancy_state SET role='controller', config_revision=3 WHERE id=1;
		INSERT INTO redundancy_nodes(node_id, name, secret_ciphertext, config_revision)
		VALUES ('one','Kitchen','fixture',1), ('two','Office','fixture',3)`)
	if err != nil {
		t.Fatal(err)
	}
	story := func() map[string]any {
		t.Helper()
		counts, err := dashboardCountsSummary(context.Background(), database, "2000-01-01")
		if err != nil {
			t.Fatal(err)
		}
		stories := dashboardStories(dashboardHighlights{replicasBehind: counts.replicasBehind, behindName: counts.behindName, dnsMetricsAvailable: true})
		if len(stories) != 1 {
			t.Fatalf("unexpected stories: %v", stories)
		}
		return stories[0]
	}
	if warning := story(); warning["section"] != "redundancy" || warning["title"] != "Backup server Kitchen is behind." || warning["tone"] != "warning" {
		t.Fatalf("missing replica warning: %v", warning)
	}
	if _, err := database.Exec(`UPDATE redundancy_nodes SET config_revision=3 WHERE node_id='one'`); err != nil {
		t.Fatal(err)
	}
	if recovered := story(); recovered["tone"] != "success" {
		t.Fatalf("warning persisted after catch-up: %v", recovered)
	}
}
