package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aliking1367/next/internal/app/tsin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// fakeTSin is the provider and the machine's tunnel in one: datacenters that
// may be up or down with their own country ports, and a tunnel whose
// handshake a restart or a registration may or may not bring back.
type fakeTSin struct {
	mu            sync.Mutex
	up            map[string]bool
	ports         map[string]map[string]int
	healthy       bool
	restartHeals  bool
	registerHeals bool
	heldSession   bool // /register answers "in use" until /disconnect
	restarts      int
	registered    []string
	disconnected  []string
}

func (f *fakeTSin) server(t *testing.T) *httptest.Server {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
		dc, path := parts[0], "/"+parts[1]
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.up[dc] {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"down"}`))
			return
		}
		switch path {
		case "/status":
			proxies := []string{}
			for country, port := range f.ports[dc] {
				proxies = append(proxies, fmt.Sprintf(`"%d":{"country":%q}`, port, country))
			}
			fmt.Fprintf(w, `{"status":"success","is_active":1,"active_proxies":{%s}}`, strings.Join(proxies, ","))
		case "/register":
			f.registered = append(f.registered, dc)
			if f.heldSession {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error": "Token is currently in use on another online server. Stop it first to switch IPs."}`))
				return
			}
			if f.registerHeals {
				f.healthy = true
			}
			fmt.Fprintf(w, `{"status":"success","server_wg_ip":"10.0.0.1","wg_conf":"[Interface]\nPrivateKey = %s\nAddress = 10.66.0.2/32\n[Peer]\nPublicKey = %s\nEndpoint = 192.0.2.10:51820\nAllowedIPs = 10.0.0.1/32\n"}`, key, key)
		case "/disconnect":
			f.disconnected = append(f.disconnected, dc)
			f.heldSession = false
			_, _ = w.Write([]byte(`{"status":"success"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (f *fakeTSin) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	command := name + " " + strings.Join(args, " ")
	switch {
	case strings.HasPrefix(command, "wg show"):
		when := time.Now().Add(-10 * time.Minute)
		if f.healthy {
			when = time.Now()
		}
		return []byte(fmt.Sprintf("peer\t%d\n", when.Unix())), nil
	case strings.HasPrefix(command, "systemctl restart"):
		f.restarts++
		if f.restartHeals {
			f.healthy = true
		}
	}
	return nil, nil
}

func setupTSinWatch(t *testing.T, fake *fakeTSin) (*Server, *tsinWatch) {
	t.Helper()
	tsinRepairWait = 1500 * time.Millisecond
	tsinReleaseWait = 0
	t.Cleanup(func() { tsinRepairWait, tsinReleaseWait = 20*time.Second, 5*time.Second })
	server, _ := testBotMigratedServer(t)
	srv := fake.server(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tsin.Interface+".conf"), []byte("[Peer]\nAllowedIPs = 10.0.0.1/32\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watch := &tsinWatch{
		tunnel: tsin.Tunnel{Dir: dir, Run: fake.run, Installed: func() bool { return true }},
		client: tsin.Client{HTTP: srv.Client(), BaseURL: func(dc string) string { return srv.URL + "/" + dc }},
	}
	settings := tsinSettings{
		Token: "token-123456", DC: "dc1", RegisteredDC: "dc1", Countries: []string{"Germany"},
		Target: xrayconfig.MasterTargetID, Transport: xrayconfig.TSinTransportTCP,
		LastAppliedAt: sql.NullTime{Time: time.Now().UTC(), Valid: true},
	}
	if err := server.saveTSinSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	return server, watch
}

func checks(server *Server, watch *tsinWatch, n int) {
	for i := 0; i < n; i++ {
		server.checkTSinTunnel(context.Background(), watch)
	}
}

func TestTSinWatchLeavesAHealthyTunnelAlone(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, healthy: true}
	server, watch := setupTSinWatch(t, fake)
	checks(server, watch, 5)
	if fake.restarts != 0 || len(fake.registered) != 0 {
		t.Fatalf("a healthy tunnel was touched: restarts=%d registered=%v", fake.restarts, fake.registered)
	}
	if watch.ports["germany"] != 5001 {
		t.Fatalf("ports not recorded: %v", watch.ports)
	}
}

func TestTSinWatchWaitsTwoChecksThenRestarts(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, restartHeals: true}
	server, watch := setupTSinWatch(t, fake)
	checks(server, watch, 1)
	if fake.restarts != 0 {
		t.Fatal("one failed check must not act yet")
	}
	checks(server, watch, 1)
	if fake.restarts != 1 || len(fake.registered) != 0 {
		t.Fatalf("restarts=%d registered=%v, want one restart only", fake.restarts, fake.registered)
	}
	checks(server, watch, 1)
	if watch.down || watch.failures != 0 {
		t.Fatalf("watch did not see the recovery: %+v", watch)
	}
}

func TestTSinWatchRegistersAgainOnTheSameDatacenter(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, registerHeals: true}
	server, watch := setupTSinWatch(t, fake)
	checks(server, watch, 2)
	if len(fake.registered) != 1 || fake.registered[0] != "dc1" {
		t.Fatalf("registered = %v, want dc1 again", fake.registered)
	}
	settings, _ := server.loadTSinSettings(context.Background())
	if settings.RegisteredDC != "dc1" || settings.DC != "dc1" {
		t.Fatalf("settings moved: %+v", settings)
	}
}

// On 2026-11-05 T.Sin kept a node's dead session online for about ten
// minutes and refused every registration as "in use" meanwhile.
func TestTSinWatchEndsAStaleSessionAndRegistersAgain(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, registerHeals: true, heldSession: true}
	server, watch := setupTSinWatch(t, fake)
	checks(server, watch, 2)
	if len(fake.disconnected) != 1 || fake.disconnected[0] != "dc1" {
		t.Fatalf("disconnected = %v, want the stale session on dc1 ended", fake.disconnected)
	}
	if len(fake.registered) != 2 || !fake.healthy {
		t.Fatalf("registered = %v healthy = %v, want a second registration that worked", fake.registered, fake.healthy)
	}
}

func TestTSinWatchMovesOnlyToADatacenterWithTheSamePorts(t *testing.T) {
	fake := &fakeTSin{
		up:    map[string]bool{"dc1": true, "dc2": true, "dc3": true},
		ports: map[string]map[string]int{"dc1": {"Germany": 5001}, "dc2": {"Germany": 7000}, "dc3": {"Germany": 5001}},
	}
	server, watch := setupTSinWatch(t, fake)
	// Healthy first, so the watch learns the ports in use.
	fake.healthy = true
	checks(server, watch, 1)
	// dc1 dies with the tunnel; only a registration elsewhere heals it.
	fake.mu.Lock()
	fake.healthy, fake.up["dc1"], fake.registerHeals = false, false, true
	fake.mu.Unlock()
	checks(server, watch, 2)

	if len(fake.registered) != 1 || fake.registered[0] != "dc3" {
		t.Fatalf("registered = %v, want dc3 (dc2 has other ports)", fake.registered)
	}
	settings, _ := server.loadTSinSettings(context.Background())
	if settings.DC != "dc3" || settings.RegisteredDC != "dc3" {
		t.Fatalf("settings = dc %s registered %s, want dc3", settings.DC, settings.RegisteredDC)
	}
}

func TestTSinWatchReportsInsteadOfMovingToOtherPorts(t *testing.T) {
	fake := &fakeTSin{
		up:    map[string]bool{"dc1": true, "dc2": true},
		ports: map[string]map[string]int{"dc1": {"Germany": 5001}, "dc2": {"Germany": 7000}},
	}
	server, watch := setupTSinWatch(t, fake)
	fake.healthy = true
	checks(server, watch, 1)
	fake.mu.Lock()
	fake.healthy, fake.up["dc1"] = false, false
	fake.mu.Unlock()
	checks(server, watch, 2)

	if len(fake.registered) != 0 {
		t.Fatalf("registered = %v, want no move", fake.registered)
	}
	if !strings.Contains(watch.lastReport, "dc2") || !strings.Contains(watch.lastReport, "other ports") {
		t.Fatalf("report = %q", watch.lastReport)
	}
	settings, _ := server.loadTSinSettings(context.Background())
	if settings.DC != "dc1" {
		t.Fatalf("settings moved to %s", settings.DC)
	}
}

func TestTSinWatchStaysOutOfAnApply(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, restartHeals: true}
	server, watch := setupTSinWatch(t, fake)
	tsinApplyMu.Lock()
	checks(server, watch, 4)
	tsinApplyMu.Unlock()
	if fake.restarts != 0 || watch.failures != 0 {
		t.Fatalf("the watch ran during an apply: restarts=%d failures=%d", fake.restarts, watch.failures)
	}
}

func TestTSinWatchIgnoresCountriesServedFromANode(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Germany": 5001}}, restartHeals: true}
	server, watch := setupTSinWatch(t, fake)
	settings, _ := server.loadTSinSettings(context.Background())
	settings.Target = "node:3"
	if err := server.saveTSinSettings(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	checks(server, watch, 4)
	if fake.restarts != 0 {
		t.Fatal("the panel must not touch a tunnel that lives on a node")
	}
}

func TestTSinAccountAlerts(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	proxies := []tsin.Proxy{{Port: 5001, Country: "Germany"}}
	quiet := tsin.Status{DataUsedGB: 10, DataLimitGB: 100, ExpiryDate: "2026-12-30T00:00:00", Proxies: proxies}
	for key, alert := range tsinAccountAlerts(quiet, []string{"Germany"}, now) {
		if alert != nil {
			t.Fatalf("a healthy account raised %s: %v", key, alert)
		}
	}

	busy := tsin.Status{DataUsedGB: 95, DataLimitGB: 100, ExpiryDate: "2026-11-05T10:00:00", ActiveKnown: true, Proxies: proxies}
	alerts := tsinAccountAlerts(busy, []string{"Germany", "Italy"}, now)
	if alerts["data"] == nil || !strings.Contains(alerts["data"][0], "رو به اتمام") {
		t.Fatalf("data = %v", alerts["data"])
	}
	if alerts["expiry"] == nil || alerts["inactive"] == nil {
		t.Fatalf("expiry=%v inactive=%v", alerts["expiry"], alerts["inactive"])
	}
	if alerts["missing"] == nil || !strings.Contains(alerts["missing"][1], "Italy") {
		t.Fatalf("missing = %v", alerts["missing"])
	}

	// No is_active in the answer is not "inactive".
	unknown := tsin.Status{Proxies: proxies}
	if alerts := tsinAccountAlerts(unknown, []string{"Germany"}, now); alerts["inactive"] != nil {
		t.Fatal("an answer without is_active must not warn")
	}
}

func TestTSinAccountWarningsAreNotRepeatedWithinADay(t *testing.T) {
	fake := &fakeTSin{up: map[string]bool{"dc1": true}, ports: map[string]map[string]int{"dc1": {"Italy": 5001}}, healthy: true}
	server, watch := setupTSinWatch(t, fake)
	server.checkTSinAccount(context.Background(), tsinSettings{Token: "token-123456", RegisteredDC: "dc1", Countries: []string{"Germany"}}, watch.client, watch)
	first := watch.alerted["missing"]
	if first.IsZero() {
		t.Fatal("a missing country should be warned about")
	}
	server.checkTSinAccount(context.Background(), tsinSettings{Token: "token-123456", RegisteredDC: "dc1", Countries: []string{"Germany"}}, watch.client, watch)
	if !watch.alerted["missing"].Equal(first) {
		t.Fatal("the same warning was sent again within a day")
	}
	// Once it no longer holds, it is forgotten, so a later return warns again.
	server.checkTSinAccount(context.Background(), tsinSettings{Token: "token-123456", RegisteredDC: "dc1", Countries: []string{"Italy"}}, watch.client, watch)
	if _, ok := watch.alerted["missing"]; ok {
		t.Fatal("a cleared warning should be forgotten")
	}
}
