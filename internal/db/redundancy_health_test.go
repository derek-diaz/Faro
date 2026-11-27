package db

import (
	"path/filepath"
	"testing"
)

func TestRedundancyHealthUpgradePreservesPairedReplicas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faro.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.Exec(`ALTER TABLE redundancy_nodes DROP COLUMN behind_since;
		ALTER TABLE redundancy_nodes DROP COLUMN dns_report;
		UPDATE redundancy_state SET role='controller', config_revision=3 WHERE id=1;
		INSERT INTO redundancy_nodes(node_id,name,secret_ciphertext,config_revision) VALUES
			('one','Utility room','existing',2),('two','Office','existing',3);
		DELETE FROM faro_schema_migrations WHERE version>=16;
		PRAGMA user_version=15;
		UPDATE settings SET value='15' WHERE key='database_schema_version'`)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var correct int
	err = upgraded.DB.QueryRow(`SELECT count(*) FROM redundancy_nodes
		WHERE secret_ciphertext='existing' AND dns_report IS NULL AND
		((node_id='one' AND behind_since IS NOT NULL) OR (node_id='two' AND behind_since IS NULL))`).Scan(&correct)
	if err != nil || correct != 2 {
		t.Fatalf("upgraded replica state = %d, %v", correct, err)
	}
}
