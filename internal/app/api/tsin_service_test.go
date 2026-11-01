package api

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestTSinServiceIsFoundByItsHostsAfterARename(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "svc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(`CREATE TABLE services (id INTEGER PRIMARY KEY, name TEXT);
CREATE TABLE hosts (id INTEGER PRIMARY KEY, inbound_tag TEXT);
CREATE TABLE service_hosts (service_id INTEGER, host_id INTEGER);`); err != nil {
		t.Fatal(err)
	}

	if id, err := findTSinServiceID(ctx, db); err != nil || id != 0 {
		t.Fatalf("empty panel: id=%d err=%v", id, err)
	}

	if _, err := db.Exec(`INSERT INTO services VALUES (1, 'Main'), (2, 'T.Sin Countries');`); err != nil {
		t.Fatal(err)
	}
	if id, _ := findTSinServiceID(ctx, db); id != 2 {
		t.Fatalf("legacy name not picked up: %d", id)
	}

	// Renamed, and the main service also carries one country host.
	if _, err := db.Exec(`UPDATE services SET name = 'VIP' WHERE id = 2;
INSERT INTO hosts VALUES (10, 'tsin-germany'), (11, 'tsin-turkey'), (12, 'auto-reality');
INSERT INTO service_hosts VALUES (1, 12), (1, 10), (2, 10), (2, 11);`); err != nil {
		t.Fatal(err)
	}
	if id, _ := findTSinServiceID(ctx, db); id != 2 {
		t.Fatalf("renamed service not found by its country hosts: %d", id)
	}
}
