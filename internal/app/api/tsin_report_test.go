package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const tsinNodeTarget = "node:7"

// setupTSinNodeReports is a panel whose countries run on node 7 and that can
// sign node reports.
func setupTSinNodeReports(t *testing.T) (*Server, *tsinWatch) {
	t.Helper()
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, healthy: true}
	server, watch := setupTSinWatch(t, fake)
	ctx := context.Background()
	if server.tsinReportKey(ctx) == "" {
		if _, err := server.db.ExecContext(ctx, `INSERT INTO jwt (secret_key, admin_secret_key) VALUES ('test-secret', 'test-secret')`); err != nil {
			t.Fatal(err)
		}
	}
	settings, err := server.loadTSinSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Target, settings.RegisteredDC = tsinNodeTarget, ""
	settings.Countries = []string{"Germany", "Italy"}
	if err := server.saveTSinSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	return server, watch
}

func postTSinReport(server *Server, key string, report map[string]any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(report)
	request := httptest.NewRequest(http.MethodPost, "/internal/tsin/report", strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer "+key)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func nodeAccount(ports map[string]string) map[string]any {
	proxies := map[string]any{}
	for port, country := range ports {
		proxies[port] = map[string]any{"country": country}
	}
	return map[string]any{"status": "success", "client_name": "seller", "data_used_gb": 12.5, "data_limit_gb": 0,
		"active_connections": 9, "expiry_date": "2026-11-28 21:51:48", "is_active": 1, "active_proxies": proxies}
}

func TestTSinNodeReportNeedsTheKey(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	if code := postTSinReport(server, "wrong", map[string]any{"dc": "dc1"}).Code; code != http.StatusForbidden {
		t.Fatalf("a report without the key got %d", code)
	}
	if server.tsinNode.load(tsinNodeTarget) != nil {
		t.Fatal("a refused report was kept")
	}
	key := server.tsinReportKey(context.Background())
	if code := postTSinReport(server, key, map[string]any{"dc": "dc1", "handshake_age": 30}).Code; code != http.StatusOK {
		t.Fatalf("a signed report got %d", code)
	}
	if report := server.tsinNode.load(tsinNodeTarget); report == nil || !report.tunnelUp() {
		t.Fatalf("report = %+v", report)
	}
}

// T.Sin answers the account only to the node holding the session, so the
// dashboard shows what the node reported instead of the panel's "in use".
func TestTSinViewShowsTheAccountTheNodeReported(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	key := server.tsinReportKey(context.Background())
	postTSinReport(server, key, map[string]any{
		"dc": "dc1", "handshake_age": 40,
		"account": nodeAccount(map[string]string{"1080": "Germany", "1093": "Italy"}),
		"ports":   map[string]bool{"1080": true, "1093": false}, "ports_at": time.Now().Unix(),
	})
	settings, _ := server.loadTSinSettings(context.Background())
	got := map[string]any{"tunnel": map[string]any{"up": false}}
	server.tsinAccountView(context.Background(), settings, got)
	body, _ := json.Marshal(got)
	var view struct {
		AccountSource string `json:"account_source"`
		AccountError  string `json:"account_error"`
		Account       struct {
			ClientName string `json:"client_name"`
		} `json:"account"`
		Tunnel     struct{ Up bool } `json:"tunnel"`
		NodeReport struct {
			PortsWorking int      `json:"ports_working"`
			PortsFailing []string `json:"ports_failing"`
		} `json:"node_report"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if view.AccountSource != "node" || view.AccountError != "" || view.Account.ClientName != "seller" || !view.Tunnel.Up {
		t.Fatalf("view = %s", body)
	}
	if view.NodeReport.PortsWorking != 1 || len(view.NodeReport.PortsFailing) != 1 || view.NodeReport.PortsFailing[0] != "Italy (1093)" {
		t.Fatalf("node_report = %+v", view.NodeReport)
	}
}

func TestTSinWatchFollowsTheNodeReports(t *testing.T) {
	server, watch := setupTSinNodeReports(t)
	key := server.tsinReportKey(context.Background())
	report := func(handshakeAge int64, ports map[string]bool) {
		// Reports arrive a minute apart; the watch looks at each new one.
		time.Sleep(5 * time.Millisecond)
		postTSinReport(server, key, map[string]any{
			"dc": "dc1", "handshake_age": handshakeAge,
			"account": nodeAccount(map[string]string{"1080": "Germany", "1093": "Italy"}),
			"ports":   ports, "ports_at": time.Now().Unix(),
		})
		checks(server, watch, 1)
	}
	healthy := map[string]bool{"1080": true, "1093": true}

	report(30, healthy)
	if watch.nodeDown || watch.nodeFailing != "" {
		t.Fatalf("a healthy node raised an alert: %+v", watch)
	}
	report(600, healthy)
	if watch.nodeDown {
		t.Fatal("one stale report must not count as down yet")
	}
	report(660, healthy)
	if !watch.nodeDown {
		t.Fatal("two stale reports in a row should count as down")
	}
	report(20, map[string]bool{"1080": true, "1093": false})
	if watch.nodeDown {
		t.Fatal("the recovery was not seen")
	}
	if watch.nodeFailing != "Italy (1093)" {
		t.Fatalf("failing = %q, want Italy", watch.nodeFailing)
	}

	// A node that stops reporting is noticed.
	stale := server.tsinNode.load(tsinNodeTarget)
	stale.ReceivedAt = time.Now().Add(-2 * tsinReportFresh)
	server.tsinNode.store(stale)
	checks(server, watch, 1)
	if !watch.nodeSilent {
		t.Fatal("a silent node was not noticed")
	}
}

// The node tests every country of the account; one no user was given does
// not count as failing.
func TestTSinNodeReportCountsOnlyTheCountriesInUse(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	key := server.tsinReportKey(context.Background())
	postTSinReport(server, key, map[string]any{
		"dc": "dc1", "handshake_age": 40,
		"account": nodeAccount(map[string]string{"1080": "Germany", "1093": "Italy", "2015": "Norway"}),
		"ports":   map[string]bool{"1080": true, "1093": true, "2015": false}, "ports_at": time.Now().Unix(),
	})
	settings, _ := server.loadTSinSettings(context.Background())
	got := map[string]any{}
	server.tsinAccountView(context.Background(), settings, got)
	report := got["node_report"].(map[string]any)
	if report["ports_total"] != 2 || report["ports_working"] != 2 || len(report["ports_failing"].([]string)) != 0 {
		t.Fatalf("node_report = %v, want 2 of 2 with Norway, unused, left out", report)
	}
}

// Apply needs the account's country list; for a node holding the session it
// comes from the node's report, since T.Sin refuses the panel as "in use".
func TestTSinAccountFromNodeIsTheFreshReport(t *testing.T) {
	server, _ := setupTSinNodeReports(t)
	if _, ok := server.tsinAccountFromNode(tsinNodeTarget); ok {
		t.Fatal("an account came from a node that never reported")
	}
	key := server.tsinReportKey(context.Background())
	postTSinReport(server, key, map[string]any{
		"dc": "dc1", "handshake_age": 40,
		"account": nodeAccount(map[string]string{"1080": "Germany", "1093": "Italy"}),
	})
	status, ok := server.tsinAccountFromNode(tsinNodeTarget)
	if !ok || len(status.Proxies) != 2 {
		t.Fatalf("status = %+v ok = %v", status, ok)
	}
	if _, ok := server.tsinAccountFromNode("master"); ok {
		t.Fatal("the panel's own tunnel must not borrow a node's report")
	}
}

func TestTSinDirectAddressAcceptsAnIPOrAHostName(t *testing.T) {
	for input, want := range map[string]string{
		"": "", " 203.0.113.7 ": "203.0.113.7", "2001:db8::1": "2001:db8::1", "Direct.Example.com": "direct.example.com",
	} {
		if got, err := normalizeTSinDirectAddress(input); err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", input, got, err, want)
		}
	}
	for _, bad := range []string{"203.0.113.7:443", "http://x.example.com", "not a host", "a;b.com"} {
		if _, err := normalizeTSinDirectAddress(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
