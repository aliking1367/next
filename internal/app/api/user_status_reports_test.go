package api

import (
	"context"
	"testing"
	"time"
)

func TestAutomaticStatusChangesReportsEachChangeOnce(t *testing.T) {
	server, db := testAdminServer(t)
	for _, statement := range []string{
		`ALTER TABLE users ADD COLUMN data_limit BIGINT NULL`,
		`ALTER TABLE users ADD COLUMN expire BIGINT NULL`,
		`INSERT INTO admins (id, username, role, status, users_usage, lifetime_usage, created_traffic, deleted_users_usage) VALUES (7, 'reseller', 'standard', 'active', 0, 0, 0, 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	start := time.Now().UTC()
	old := start.Add(-time.Hour).Format("2006-01-02 15:04:05")
	recent := start.Format("2006-01-02 15:04:05.000000")
	for _, row := range []struct {
		id      int64
		name    string
		status  string
		changed string
	}{
		{1, "limited_now", "limited", recent},
		{2, "expired_now", "expired", recent},
		{3, "limited_long_ago", "limited", old},
		{4, "active_now", "active", recent},
	} {
		if _, err := db.Exec(`INSERT INTO users (id, username, admin_id, status, last_status_change, data_limit) VALUES (?, ?, 7, ?, ?, 1024)`, row.id, row.name, row.status, row.changed); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]time.Time{}
	reports, next := server.automaticStatusChanges(context.Background(), start, seen)
	if len(reports) != 2 {
		t.Fatalf("reports = %+v, want the two users changed now", reports)
	}
	for _, report := range reports {
		if report.Owner != "reseller" || report.Actor != "system" || report.DataLimit == nil || *report.DataLimit != 1024 {
			t.Fatalf("report = %+v", report)
		}
	}
	if reports[0].Status != "limited" || reports[1].Status != "expired" {
		t.Fatalf("statuses = %s, %s", reports[0].Status, reports[1].Status)
	}

	again, _ := server.automaticStatusChanges(context.Background(), next, seen)
	if len(again) != 0 {
		t.Fatalf("second tick reported %+v again", again)
	}
}
