package db

import (
	"path/filepath"
	"testing"
)

// The busy timeout must reach the driver: without it, two writes at once
// fail immediately with SQLITE_BUSY instead of one waiting for the other.
func TestSQLiteConnectionsWaitWhenTheDatabaseIsBusy(t *testing.T) {
	path := filepath.ToSlash(filepath.Join(t.TempDir(), "panel.sqlite3"))
	pool, err := Open("sqlite:///" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.DB.Close()
	var timeout int
	if err := pool.DB.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 30000 {
		t.Fatalf("busy_timeout = %d, want 30000", timeout)
	}
}
