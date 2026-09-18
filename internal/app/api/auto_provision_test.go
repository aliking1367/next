//go:build cgo

package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

func TestCoreAutoConfigureCreatesProtocolsAndBundlesService(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/core/auto-configure", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ServiceID   int64 `json:"service_id"`
		ServiceName string
		Protocols   []struct {
			Protocol string `json:"protocol"`
			Tag      string `json:"tag"`
			Port     int    `json:"port"`
			Created  bool   `json:"created"`
		} `json:"protocols"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Protocols) != 4 {
		t.Fatalf("expected 4 protocols, got %d: %#v", len(resp.Protocols), resp.Protocols)
	}
	if resp.ServiceID == 0 {
		t.Fatalf("expected a non-zero service id")
	}
	wantTags := map[string]bool{
		"auto-vless-reality": true, "auto-trojan-reality": true,
		"auto-hysteria2": true, "auto-shadowsocks": true,
	}
	for _, protocol := range resp.Protocols {
		if !protocol.Created {
			t.Errorf("expected %s to be newly created", protocol.Tag)
		}
		if !wantTags[protocol.Tag] {
			t.Errorf("unexpected tag %q", protocol.Tag)
		}
		delete(wantTags, protocol.Tag)
	}
	if len(wantTags) != 0 {
		t.Fatalf("missing expected tags: %#v", wantTags)
	}

	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM services WHERE id = %d AND name = 'Best Protocols (Auto)'`, resp.ServiceID), 1)
	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM service_hosts WHERE service_id = %d`, resp.ServiceID), 4)

	// Re-running is idempotent: same service, no duplicate hosts/rows, and any
	// host an admin manually attached in the meantime survives.
	if _, err := db.Exec(`INSERT INTO hosts (remark, address, inbound_tag) VALUES ('manual', '1.2.3.4', 'manual-tag')`); err != nil {
		t.Fatal(err)
	}
	var manualHostID int64
	if err := db.QueryRow(`SELECT id FROM hosts WHERE inbound_tag = 'manual-tag'`).Scan(&manualHostID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO service_hosts (service_id, host_id, sort) VALUES (?, ?, 99)`, resp.ServiceID, manualHostID); err != nil {
		t.Fatal(err)
	}

	rec = adminJSONRequest(t, server, http.MethodPost, "/api/core/auto-configure", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("second auto-configure status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp2 struct {
		ServiceID int64 `json:"service_id"`
		Protocols []struct {
			Created bool `json:"created"`
		} `json:"protocols"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode second response: %v body=%s", err, rec.Body.String())
	}
	if resp2.ServiceID != resp.ServiceID {
		t.Fatalf("expected the same service to be reused, got %d vs %d", resp2.ServiceID, resp.ServiceID)
	}
	for _, protocol := range resp2.Protocols {
		if protocol.Created {
			t.Errorf("expected protocols to be reused, not recreated, on second run")
		}
	}
	assertMasterAPICount(t, db, `SELECT COUNT(*) FROM services WHERE name = 'Best Protocols (Auto)'`, 1)
	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM service_hosts WHERE service_id = %d`, resp.ServiceID), 5)
}

func TestCoreVerifyProtocolsReportsPerProtocolStatus(t *testing.T) {
	server, _, token := testAutoProvisionServer(t)

	// Nothing is provisioned yet: there is nothing to verify.
	rec := adminJSONRequest(t, server, http.MethodPost, "/api/core/verify-protocols", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify (empty) status=%d body=%s", rec.Code, rec.Body.String())
	}
	var empty struct {
		Results []struct {
			Tag string `json:"tag"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty verify response: %v body=%s", err, rec.Body.String())
	}
	if len(empty.Results) != 0 {
		t.Fatalf("expected no results before provisioning, got %#v", empty.Results)
	}

	if rec = adminJSONRequest(t, server, http.MethodPost, "/api/core/auto-configure", token, ""); rec.Code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = adminJSONRequest(t, server, http.MethodPost, "/api/core/verify-protocols", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			Port     int    `json:"port"`
			Status   string `json:"status"`
			Detail   string `json:"detail"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode verify response: %v body=%s", err, rec.Body.String())
	}
	if len(resp.Results) != 4 {
		t.Fatalf("expected 4 results, got %d: %#v", len(resp.Results), resp.Results)
	}
	for _, result := range resp.Results {
		if result.Port == 0 {
			t.Errorf("%s: expected a port in the result", result.Tag)
		}
		if result.Detail == "" {
			t.Errorf("%s: expected an explanatory detail", result.Tag)
		}
		// No Xray is running in tests, so TCP inbounds must honestly report a
		// failure rather than claiming success, and Hysteria2 is not probeable.
		want := "failed"
		if result.Protocol == "hysteria" {
			want = "skipped"
		}
		if result.Status != want {
			t.Errorf("%s: expected status %q with no Xray running, got %q (%s)", result.Tag, want, result.Status, result.Detail)
		}
	}
}

// testAutoProvisionServer extends the base admin test fixture with the
// columns AutoProvisionBestProtocols/CreateInbound and service creation need
// (usage_coefficient, and the fuller services/hosts/service_hosts shape),
// following the same local-schema-patch convention as testServiceServer.
func testAutoProvisionServer(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	server, db := testAdminServer(t)
	statements := []string{
		`ALTER TABLE inbounds ADD COLUMN usage_coefficient REAL NOT NULL DEFAULT 1`,
		`DROP TABLE services`,
		`DROP TABLE hosts`,
		`DROP TABLE service_hosts`,
		`CREATE TABLE services (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT UNIQUE NOT NULL,
			description TEXT NULL,
			used_traffic BIGINT DEFAULT 0,
			lifetime_used_traffic BIGINT DEFAULT 0,
			users_usage BIGINT DEFAULT 0,
			created_at DATETIME NULL,
			updated_at DATETIME NULL
		)`,
		`CREATE TABLE hosts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			remark TEXT,
			address TEXT,
			port BIGINT NULL,
			path TEXT NULL,
			sni TEXT NULL,
			host TEXT NULL,
			security TEXT NOT NULL DEFAULT 'inbound_default',
			alpn TEXT NOT NULL DEFAULT 'none',
			fingerprint TEXT NOT NULL DEFAULT 'none',
			inbound_tag TEXT,
			allowinsecure INTEGER NULL,
			is_disabled INTEGER DEFAULT 0,
			mux_enable INTEGER NOT NULL DEFAULT 0,
			fragment_setting TEXT NULL,
			noise_setting TEXT NULL,
			random_user_agent INTEGER NOT NULL DEFAULT 0,
			use_sni_as_host INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE service_hosts (
			service_id INTEGER,
			host_id INTEGER,
			sort BIGINT DEFAULT 0,
			created_at DATETIME NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	insertMasterAPIAdmin(t, db, 1, "sudo-admin", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	return server, db, adminBearerToken(t, server, "sudo-admin", "pass123")
}
