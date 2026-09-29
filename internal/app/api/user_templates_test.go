package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	"github.com/aliking1367/next/internal/app/usage"
	userapp "github.com/aliking1367/next/internal/app/user"
)

func testUserTemplateServer(t *testing.T) (*Server, *sql.DB, string) {
	t.Helper()
	server, db := testAdminServer(t)
	server.usageService = usage.NewService(usage.NewRepository(db, "sqlite"))
	server.userService = userapp.NewService(userapp.NewRepository(db, "sqlite"))
	for _, statement := range []string{
		`DROP TABLE IF EXISTS services`,
		`DROP TABLE IF EXISTS hosts`,
		`DROP TABLE IF EXISTS service_hosts`,
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
			id INTEGER PRIMARY KEY,
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
		`INSERT INTO hosts (id, inbound_tag, remark, address, port, security, alpn, fingerprint, is_disabled, mux_enable, random_user_agent, use_sni_as_host) VALUES
			(1, 'vless-in', 'main', 'example.com', 443, 'inbound_default', 'none', 'none', 0, 0, 0, 0),
			(2, 'vmess-in', 'second', 'example.org', 8443, 'inbound_default', 'none', 'none', 0, 0, 0, 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO xray_config (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`,
		`{"inbounds":[{"tag":"vless-in","protocol":"vless","port":443,"settings":{"clients":[]},"streamSettings":{"network":"tcp","security":"tls"}},{"tag":"vmess-in","protocol":"vmess","port":8443,"settings":{"clients":[]},"streamSettings":{"network":"tcp","security":"tls"}}]}`); err != nil {
		t.Fatal(err)
	}
	insertMasterAPIAdmin(t, db, 1, "owner", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 2, "seller", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	token := adminBearerToken(t, server, "owner", "pass123")
	if _, err := db.Exec(`CREATE TABLE reseller_plan_templates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name VARCHAR(64) NOT NULL,
		data_limit BIGINT NOT NULL DEFAULT 0,
		expire_duration BIGINT NOT NULL DEFAULT 0,
		username_prefix VARCHAR(20) NULL,
		username_suffix VARCHAR(20) NULL,
		service_id INTEGER NULL,
		inbounds TEXT NULL,
		created_at DATETIME NULL,
		updated_at DATETIME NULL
	)`); err != nil {
		t.Fatal(err)
	}
	return server, db, token
}

func decodeUserTemplate(t *testing.T, body []byte) userTemplateResponse {
	t.Helper()
	var template userTemplateResponse
	if err := json.Unmarshal(body, &template); err != nil {
		t.Fatalf("decode template: %v (%s)", err, body)
	}
	return template
}

// A reseller bot reads the plan list right after it logs in. An empty panel
// has to answer with an empty list, not a 404, or the bot reports that it
// cannot talk to the panel at all.
func TestUserTemplateListIsEmptyArrayNotMissing(t *testing.T) {
	server, _, token := testUserTemplateServer(t)

	rec := adminJSONRequest(t, server, http.MethodGet, "/api/user_template", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "[]\n" && got != "[]" {
		t.Fatalf("empty panel must answer with an empty list, got %q", got)
	}
}

func TestUserTemplateCreateReadUpdateDelete(t *testing.T) {
	server, _, token := testUserTemplateServer(t)

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/user_template", token,
		`{"name":"1 Month 50GB","data_limit":53687091200,"expire_duration":2592000,"username_prefix":"m_"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	created := decodeUserTemplate(t, rec.Body.Bytes())
	if created.ID == 0 || created.Name != "1 Month 50GB" || created.DataLimit != 53687091200 || created.ExpireDuration != 2592000 {
		t.Fatalf("unexpected created template %+v", created)
	}
	if created.UsernamePrefix == nil || *created.UsernamePrefix != "m_" {
		t.Errorf("username_prefix = %v", created.UsernamePrefix)
	}
	if created.Inbounds == nil {
		t.Error("inbounds must be an object, not null, or clients break on it")
	}

	rec = adminJSONRequest(t, server, http.MethodGet, "/api/user_template", token, "")
	var list []userTemplateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %s (%v)", rec.Body.String(), err)
	}

	// A partial update must not blank the fields it leaves out.
	rec = adminJSONRequest(t, server, http.MethodPut, "/api/user_template/1", token, `{"data_limit":107374182400}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body.String())
	}
	updated := decodeUserTemplate(t, rec.Body.Bytes())
	if updated.DataLimit != 107374182400 {
		t.Errorf("data_limit = %d", updated.DataLimit)
	}
	if updated.Name != "1 Month 50GB" || updated.ExpireDuration != 2592000 {
		t.Errorf("a partial update dropped untouched fields: %+v", updated)
	}
	if updated.UsernamePrefix == nil || *updated.UsernamePrefix != "m_" {
		t.Errorf("username_prefix was dropped: %v", updated.UsernamePrefix)
	}

	rec = adminJSONRequest(t, server, http.MethodGet, "/api/user_template/1", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d", rec.Code)
	}

	rec = adminJSONRequest(t, server, http.MethodDelete, "/api/user_template/1", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
	rec = adminJSONRequest(t, server, http.MethodGet, "/api/user_template/1", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted template still readable: %d", rec.Code)
	}
}

// A template points at a service, which is how this panel says which hosts a
// plan hands out. Clients read the protocols from `inbounds`, so they are
// derived from that service rather than left empty.
func TestUserTemplateDerivesInboundsFromItsService(t *testing.T) {
	server, db, token := testUserTemplateServer(t)

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/v2/services", token,
		`{"name":"Basic","hosts":[{"host_id":1},{"host_id":2}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("service create status = %d body=%s", rec.Code, rec.Body.String())
	}
	var service struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &service); err != nil {
		t.Fatal(err)
	}

	rec = adminJSONRequest(t, server, http.MethodPost, "/api/user_template", token,
		`{"name":"Basic plan","data_limit":0,"expire_duration":0,"service_id":`+strconv.FormatInt(service.ID, 10)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	created := decodeUserTemplate(t, rec.Body.Bytes())
	if created.ServiceID == nil || *created.ServiceID != service.ID {
		t.Fatalf("service_id = %v", created.ServiceID)
	}
	if created.ServiceName == nil || *created.ServiceName != "Basic" {
		t.Errorf("service_name = %v", created.ServiceName)
	}
	var linked int
	if err := db.QueryRow(`SELECT COUNT(*) FROM service_hosts WHERE service_id = ?`, service.ID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked != 2 {
		t.Fatalf("service should hold both hosts, got %d", linked)
	}
	// The protocol each tag is grouped under comes from the shared Xray
	// config; what matters here is that every tag the service hands out is
	// reported, because that is the list a bot reads off the plan.
	tags := map[string]bool{}
	for _, group := range created.Inbounds {
		for _, tag := range group {
			tags[tag] = true
		}
	}
	if !tags["vless-in"] || !tags["vmess-in"] {
		t.Errorf("inbounds = %v, want both of the service's tags", created.Inbounds)
	}
}

// Explicit inbounds sent by a client are stored and returned unchanged, so a
// bot that round-trips its own value does not see it rewritten.
func TestUserTemplateKeepsExplicitInbounds(t *testing.T) {
	server, _, token := testUserTemplateServer(t)

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/user_template", token,
		`{"name":"Custom","inbounds":{"vless":["my-tag"]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}
	created := decodeUserTemplate(t, rec.Body.Bytes())
	if got := created.Inbounds["vless"]; len(got) != 1 || got[0] != "my-tag" {
		t.Errorf("inbounds = %v", created.Inbounds)
	}
}

func TestUserTemplateValidationAndPermissions(t *testing.T) {
	server, _, token := testUserTemplateServer(t)
	sellerToken := adminBearerToken(t, server, "seller", "pass123")

	for name, body := range map[string]string{
		"empty name":         `{"name":"   "}`,
		"negative limit":     `{"name":"x","data_limit":-1}`,
		"negative duration":  `{"name":"x","expire_duration":-5}`,
		"overlong prefix":    `{"name":"x","username_prefix":"012345678901234567890"}`,
		"unknown service_id": `{"name":"x","service_id":9999}`,
	} {
		rec := adminJSONRequest(t, server, http.MethodPost, "/api/user_template", token, body)
		if rec.Code != http.StatusUnprocessableEntity && rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d body=%s", name, rec.Code, rec.Body.String())
		}
	}

	// A non-sudo admin may read the plans but must not change them.
	rec := adminJSONRequest(t, server, http.MethodGet, "/api/user_template", sellerToken, "")
	if rec.Code != http.StatusOK {
		t.Errorf("standard admin list status = %d", rec.Code)
	}
	rec = adminJSONRequest(t, server, http.MethodPost, "/api/user_template", sellerToken, `{"name":"nope"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("standard admin create status = %d, want 403", rec.Code)
	}

	rec = adminJSONRequest(t, server, http.MethodGet, "/api/user_template/404", token, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown template status = %d", rec.Code)
	}
}
