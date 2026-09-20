//go:build cgo

package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

type autoConfigureTestResponse struct {
	ServiceID    int64  `json:"service_id"`
	ServiceName  string `json:"service_name"`
	Flow         string `json:"flow"`
	CDNRequested bool   `json:"cdn_requested"`
	Retired      []string
	Warning      string `json:"warning"`
	Protocols    []struct {
		Protocol string `json:"protocol"`
		Recipe   string `json:"recipe"`
		Tag      string `json:"tag"`
		Port     int    `json:"port"`
		Created  bool   `json:"created"`
	} `json:"protocols"`
	Nodes struct {
		Total     int `json:"total"`
		Connected int `json:"connected"`
		Serving   int `json:"serving"`
	} `json:"nodes"`
}

func autoConfigureRequest(t *testing.T, server *Server, token string, body string) (autoConfigureTestResponse, int, string) {
	t.Helper()
	rec := adminJSONRequest(t, server, http.MethodPost, "/api/core/auto-configure", token, body)
	var resp autoConfigureTestResponse
	if rec.Code == http.StatusOK {
		// Decode into a fresh value so omitted fields read as empty.
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v body=%s", err, rec.Body.String())
		}
	}
	return resp, rec.Code, rec.Body.String()
}

func TestCoreAutoConfigureCreatesProtocolsAndBundlesService(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)

	resp, code, body := autoConfigureRequest(t, server, token, "")
	if code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", code, body)
	}
	wantTags := map[string]string{
		"auto-reality-vision": "reality-vision",
		"auto-reality-xhttp":  "reality-xhttp",
		"auto-hysteria2-obfs": "hysteria2-obfs",
	}
	if len(resp.Protocols) != len(wantTags) {
		t.Fatalf("expected %d protocols without a CDN domain, got %+v", len(wantTags), resp.Protocols)
	}
	for _, protocol := range resp.Protocols {
		if !protocol.Created {
			t.Errorf("expected %s to be newly created", protocol.Tag)
		}
		if wantTags[protocol.Tag] != protocol.Recipe {
			t.Errorf("unexpected tag/recipe %q/%q", protocol.Tag, protocol.Recipe)
		}
		delete(wantTags, protocol.Tag)
	}
	if len(wantTags) != 0 {
		t.Fatalf("missing expected tags: %#v", wantTags)
	}
	if resp.CDNRequested {
		t.Error("no CDN domain was supplied")
	}

	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM services WHERE id = %d AND name = 'Best Protocols (Auto)'`, resp.ServiceID), 1)
	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM service_hosts WHERE service_id = %d`, resp.ServiceID), 3)

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

	again, code, body := autoConfigureRequest(t, server, token, "")
	if code != http.StatusOK {
		t.Fatalf("second auto-configure status=%d body=%s", code, body)
	}
	if again.ServiceID != resp.ServiceID {
		t.Fatalf("expected the same service to be reused, got %d vs %d", again.ServiceID, resp.ServiceID)
	}
	for _, protocol := range again.Protocols {
		if protocol.Created {
			t.Errorf("expected %s to be reused, not recreated, on second run", protocol.Tag)
		}
	}
	assertMasterAPICount(t, db, `SELECT COUNT(*) FROM services WHERE name = 'Best Protocols (Auto)'`, 1)
	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM service_hosts WHERE service_id = %d`, resp.ServiceID), 4)
}

func TestCoreAutoConfigureAddsCDNInboundOnRequest(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)

	resp, code, body := autoConfigureRequest(t, server, token, `{"cdn_domain":"  CDN.Example.com "}`)
	if code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", code, body)
	}
	if !resp.CDNRequested || len(resp.Protocols) != 4 {
		t.Fatalf("expected four inbounds including the CDN one, got %+v", resp)
	}
	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM service_hosts WHERE service_id = %d`, resp.ServiceID), 4)
	var address, sni string
	if err := db.QueryRow(`SELECT address, sni FROM hosts WHERE inbound_tag = 'auto-cdn-xhttp'`).Scan(&address, &sni); err != nil {
		t.Fatal(err)
	}
	if address != "cdn.example.com" || sni != "cdn.example.com" {
		t.Errorf("the CDN host must dial the normalized domain, got %q / %q", address, sni)
	}
}

func TestCoreAutoConfigureRejectsInvalidCDNDomain(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)
	for _, body := range []string{
		`{"cdn_domain":"1.2.3.4"}`,
		`{"cdn_domain":"https://cdn.example.com"}`,
		`{"cdn_domain":"not a domain"}`,
	} {
		_, code, response := autoConfigureRequest(t, server, token, body)
		if code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d (%s)", body, code, response)
		}
	}
	// A rejected request must not have provisioned anything.
	assertMasterAPICount(t, db, `SELECT COUNT(*) FROM inbounds WHERE tag LIKE 'auto-%'`, 0)
}

func TestCoreAutoConfigureRetiresLegacyInboundsAndKeepsThemOut(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)
	for _, payload := range []map[string]any{
		{"tag": "auto-trojan-reality", "protocol": "trojan", "port": 31001, "settings": map[string]any{"clients": []any{}}},
		{"tag": "auto-shadowsocks", "protocol": "shadowsocks", "port": 31002, "settings": map[string]any{"clients": []any{}, "network": "tcp,udp"}},
	} {
		if _, err := server.configRepo.CreateInbound(context.Background(), payload); err != nil {
			t.Fatalf("seed %v: %v", payload["tag"], err)
		}
	}

	resp, code, body := autoConfigureRequest(t, server, token, "")
	if code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", code, body)
	}
	if len(resp.Retired) != 2 {
		t.Fatalf("expected both legacy inbounds to be retired, got %v", resp.Retired)
	}
	assertMasterAPICount(t, db, `SELECT COUNT(*) FROM inbounds WHERE tag IN ('auto-trojan-reality','auto-shadowsocks')`, 0)
	assertMasterAPICount(t, db, `SELECT COUNT(*) FROM hosts WHERE inbound_tag IN ('auto-trojan-reality','auto-shadowsocks')`, 0)
	assertMasterAPICount(t, db, fmt.Sprintf(`SELECT COUNT(*) FROM service_hosts WHERE service_id = %d`, resp.ServiceID), 3)
}

func TestCoreAutoConfigureAppliesVisionFlowWithoutOverridingAChoice(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)

	resp, code, body := autoConfigureRequest(t, server, token, "")
	if code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", code, body)
	}
	if resp.Flow != "xtls-rprx-vision" {
		t.Fatalf("the bundle must enable Vision for VLESS+REALITY users, got %q", resp.Flow)
	}
	var flow string
	if err := db.QueryRow(`SELECT COALESCE(flow, '') FROM services WHERE id = ?`, resp.ServiceID).Scan(&flow); err != nil {
		t.Fatal(err)
	}
	if flow != "xtls-rprx-vision" {
		t.Fatalf("flow was not stored on the service, got %q", flow)
	}
	// Enabling Vision changes what existing users must be given, so nodes are
	// told to rebuild them.
	assertMasterAPICount(t, db, `SELECT COUNT(*) FROM node_operations WHERE operation_type = 'sync_config' AND payload LIKE '%"source":"hosts"%'`, 1)

	// An admin who chose another flow keeps it.
	if _, err := db.Exec(`UPDATE services SET flow = 'xtls-rprx-vision-udp443' WHERE id = ?`, resp.ServiceID); err != nil {
		t.Fatal(err)
	}
	again, code, body := autoConfigureRequest(t, server, token, "")
	if code != http.StatusOK {
		t.Fatalf("second auto-configure status=%d body=%s", code, body)
	}
	if again.Flow != "xtls-rprx-vision-udp443" {
		t.Errorf("an admin's flow choice must be kept, got %q", again.Flow)
	}
}

func TestCoreAutoConfigureWarnsWhenNoNodeExists(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)

	autoConfigure := func() autoConfigureTestResponse {
		t.Helper()
		resp, code, body := autoConfigureRequest(t, server, token, "")
		if code != http.StatusOK {
			t.Fatalf("auto-configure status=%d body=%s", code, body)
		}
		return resp
	}

	// Xray only runs on nodes: with none registered, nothing serves the
	// inbounds and users' {SERVER_IP} has no address to resolve to.
	if resp := autoConfigure(); resp.Warning != "no_nodes" || resp.Nodes.Total != 0 {
		t.Fatalf("expected the no_nodes warning, got %+v", resp)
	}

	if _, err := db.Exec(`INSERT INTO nodes (id, name, address, status) VALUES (1, 'edge', '203.0.113.5', 'error')`); err != nil {
		t.Fatal(err)
	}
	if resp := autoConfigure(); resp.Warning != "no_connected_nodes" || resp.Nodes.Total != 1 || resp.Nodes.Connected != 0 {
		t.Fatalf("expected no_connected_nodes, got %+v", resp)
	}

	if _, err := db.Exec(`UPDATE nodes SET status = 'connected', xray_config_mode = 'custom' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if resp := autoConfigure(); resp.Warning != "no_default_config_nodes" || resp.Nodes.Connected != 1 || resp.Nodes.Serving != 0 {
		t.Fatalf("a custom-config node does not serve the shared inbounds, got %+v", resp)
	}

	if _, err := db.Exec(`UPDATE nodes SET xray_config_mode = 'default' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if resp := autoConfigure(); resp.Warning != "" || resp.Nodes.Serving != 1 {
		t.Fatalf("expected no warning once a default-config node is connected, got %+v", resp)
	}
}

func TestCoreVerifyProtocolsWithoutNodesExplainsWhy(t *testing.T) {
	server, _, token := testAutoProvisionServer(t)
	if _, code, body := autoConfigureRequest(t, server, token, ""); code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", code, body)
	}

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/core/verify-protocols", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Warning string           `json:"warning"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// With no node there is nothing to probe. Probing the panel's own
	// localhost would be wrong: the panel never runs Xray.
	if resp.Warning != "no_nodes" || len(resp.Results) != 0 {
		t.Fatalf("expected the no_nodes warning and no results, got %+v", resp)
	}
}

func TestCoreVerifyProtocolsProbesEachConnectedNodesAddress(t *testing.T) {
	server, db, token := testAutoProvisionServer(t)
	if _, code, body := autoConfigureRequest(t, server, token, `{"cdn_domain":"cdn.example.com"}`); code != http.StatusOK {
		t.Fatalf("auto-configure status=%d body=%s", code, body)
	}

	// A TLS listener stands in for the node's Xray on the primary REALITY
	// inbound's port; every other inbound port stays closed.
	inbound, err := server.configRepo.GetInbound(context.Background(), "auto-reality-vision")
	if err != nil {
		t.Fatal(err)
	}
	visionPort := 0
	switch port := inbound["port"].(type) {
	case float64:
		visionPort = int(port)
	case int:
		visionPort = port
	case int64:
		visionPort = int(port)
	case json.Number:
		parsed, _ := port.Int64()
		visionPort = int(parsed)
	default:
		t.Fatalf("unexpected port type %T", inbound["port"])
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:"+strconv.Itoa(visionPort), &tls.Config{
		Certificates: []tls.Certificate{selfSignedAPITestCertificate(t)},
	})
	if err != nil {
		t.Skipf("cannot bind the generated inbound port %d in this environment: %v", visionPort, err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				conn.Close()
			}()
		}
	}()

	if _, err := db.Exec(`INSERT INTO nodes (id, name, address, status) VALUES (1, 'edge', '127.0.0.1', 'connected')`); err != nil {
		t.Fatal(err)
	}
	// A connected node on a custom config, and a disconnected one, are not probed.
	if _, err := db.Exec(`INSERT INTO nodes (id, name, address, status, xray_config_mode) VALUES (2, 'custom', '127.0.0.1', 'connected', 'custom')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO nodes (id, name, address, status) VALUES (3, 'down', '127.0.0.1', 'error')`); err != nil {
		t.Fatal(err)
	}

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/core/verify-protocols", token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Warning string `json:"warning"`
		Results []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
			NodeID   int64  `json:"node_id"`
			NodeName string `json:"node_name"`
			Address  string `json:"address"`
			Status   string `json:"status"`
			Detail   string `json:"detail"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Warning != "" {
		t.Fatalf("unexpected warning %q", resp.Warning)
	}
	if len(resp.Results) != 4 {
		t.Fatalf("expected 4 results (4 inbounds x the one probed node), got %d: %+v", len(resp.Results), resp.Results)
	}
	for _, result := range resp.Results {
		if result.NodeID != 1 || result.NodeName != "edge" || result.Address != "127.0.0.1" {
			t.Errorf("%s: result must identify the probed node, got %+v", result.Tag, result)
		}
		want := "failed"
		switch result.Tag {
		case "auto-reality-vision":
			want = "ok"
		case "auto-hysteria2-obfs":
			want = "skipped"
		}
		if result.Status != want {
			t.Errorf("%s: expected %q, got %q (%s)", result.Tag, want, result.Status, result.Detail)
		}
	}
}

func TestLocalPortBusyDetectsAListeningPort(t *testing.T) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skipf("cannot listen in this environment: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if !localPortBusy(port) {
		t.Errorf("port %d is being listened on, expected busy", port)
	}
	listener.Close()
	if localPortBusy(port) {
		t.Errorf("port %d was released, expected free", port)
	}
}

func selfSignedAPITestCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "probe"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// testAutoProvisionServer extends the base admin test fixture with the
// columns AutoProvisionBestProtocols/CreateInbound and service creation need
// (usage_coefficient, the service flow column, and the fuller
// services/hosts/service_hosts shape), following the same local-schema-patch
// convention as testServiceServer.
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
			flow TEXT NULL,
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
			pinned_peer_cert_sha256 TEXT NULL,
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
