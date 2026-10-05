package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCDNAutomationReadyNeedsEverythingItRunsOn(t *testing.T) {
	cases := []struct {
		name   string
		config cdnAutomation
		want   bool
	}{
		{"disabled", cdnAutomation{DomainSuffix: "example.com", Token: "t"}, false},
		{"no domain", cdnAutomation{Enabled: true, Token: "t"}, false},
		{"bad domain", cdnAutomation{Enabled: true, DomainSuffix: "localhost", Token: "t"}, false},
		{"no token", cdnAutomation{Enabled: true, DomainSuffix: "example.com"}, false},
		{"blank token", cdnAutomation{Enabled: true, DomainSuffix: "example.com", Token: "   "}, false},
		{"complete", cdnAutomation{Enabled: true, DomainSuffix: "example.com", Token: "t"}, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.config.ready(); got != testCase.want {
				t.Fatalf("ready() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestCDNAutomationInboundTagFallsBackToTheAutoInbound(t *testing.T) {
	if got := (cdnAutomation{}).inboundTag(); got != defaultCDNInboundTag {
		t.Fatalf("inboundTag() = %q, want %q", got, defaultCDNInboundTag)
	}
	if got := (cdnAutomation{InboundTag: "  custom-cdn "}).inboundTag(); got != "custom-cdn" {
		t.Fatalf("inboundTag() = %q, want %q", got, "custom-cdn")
	}
}

// The token can edit the admin's whole zone, so the one thing this view must
// never do is hand it back to anyone who can read the settings.
func TestCDNAutomationViewNeverRevealsTheToken(t *testing.T) {
	const secret = "cf-token-must-not-appear"
	view := cdnAutomationView(cdnAutomation{
		Enabled:      true,
		DomainSuffix: "example.com",
		Token:        secret,
	})
	if !view.TokenSet {
		t.Fatal("token_set should report that a token is stored")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("the response carried the token: %s", encoded)
	}
}

func TestCDNAutomationRoundTripsThroughTheDatabase(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	ctx := context.Background()

	initial, err := server.loadCDNAutomation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Enabled {
		t.Fatal("automation should start turned off")
	}
	if !initial.RemoveRecordsOnDelete {
		t.Fatal("retiring records on delete should be the default")
	}

	stored := cdnAutomation{
		Enabled:               true,
		DomainSuffix:          "example.com",
		InboundTag:            "auto-cdn-xhttp",
		Token:                 "cf-token",
		RemoveRecordsOnDelete: false,
	}
	if err := server.saveCDNAutomation(ctx, stored); err != nil {
		t.Fatal(err)
	}
	loaded, err := server.loadCDNAutomation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Enabled || loaded.DomainSuffix != "example.com" || loaded.Token != "cf-token" {
		t.Fatalf("round trip lost fields: %+v", loaded)
	}
	if loaded.RemoveRecordsOnDelete {
		t.Fatal("remove_records_on_delete should have been saved as false")
	}

	// Saving must keep the single row single, or a later read would pick up
	// whichever copy happened to sort first.
	var rows int
	if err := server.db.QueryRow(`SELECT COUNT(*) FROM cdn_automation`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("cdn_automation holds %d rows, want 1", rows)
	}
}

func TestNodeCDNHostnameRemembersAndForgets(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	ctx := context.Background()
	result, err := server.db.Exec(`INSERT INTO nodes (name, address, port, api_port) VALUES (?, ?, ?, ?)`,
		"Amsterdam", "198.51.100.10", 62050, 62051)
	if err != nil {
		t.Fatal(err)
	}
	nodeID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if got := server.nodeCDNHostname(ctx, nodeID); got != "" {
		t.Fatalf("a fresh node should own no hostname, got %q", got)
	}
	if err := server.setNodeCDNHostname(ctx, nodeID, "amsterdam.example.com"); err != nil {
		t.Fatal(err)
	}
	if got := server.nodeCDNHostname(ctx, nodeID); got != "amsterdam.example.com" {
		t.Fatalf("nodeCDNHostname() = %q", got)
	}

	hostnames, err := server.cdnCurrentHostnames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hostnames[nodeID] != "amsterdam.example.com" {
		t.Fatalf("cdnCurrentHostnames() = %v", hostnames)
	}

	// A node the panel has never seen must read as empty rather than as some
	// other node's hostname.
	if got := server.nodeCDNHostname(ctx, nodeID+999); got != "" {
		t.Fatalf("unknown node returned %q", got)
	}
}

// A sync cannot run without a host to copy, and the error has to say so: an
// admin who turned the switch on and saw nothing happen needs to be told the
// one thing that is missing.
func TestSyncCDNAutomationRefusesWithoutATemplateHost(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	_, err := server.syncCDNAutomation(context.Background(), cdnAutomation{
		Enabled:      true,
		DomainSuffix: "example.com",
		Token:        "cf-token",
	})
	if err == nil {
		t.Fatal("expected an error when the inbound has no host to copy")
	}
	if !strings.Contains(err.Error(), "auto-cdn-xhttp") {
		t.Fatalf("the error should name the inbound: %v", err)
	}
}

func TestSyncCDNAutomationRefusesWithoutADomain(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	_, err := server.syncCDNAutomation(context.Background(), cdnAutomation{Enabled: true, Token: "cf-token"})
	if err == nil || !strings.Contains(err.Error(), "domain") {
		t.Fatalf("expected a domain error, got %v", err)
	}
}

func TestUpsertCDNHostRowIsIdempotent(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	ctx := context.Background()
	if _, err := server.db.Exec(`INSERT INTO inbounds (tag) VALUES (?)`, defaultCDNInboundTag); err != nil {
		t.Fatal(err)
	}
	port := int64(2087)
	path := "/492d0980e0be"
	template := hostPayload{Port: &port, Path: &path, Security: "tls", ALPN: "h2", Fingerprint: "chrome"}
	plan := cdnHostPlan{
		NodeID:   1,
		NodeName: "Amsterdam",
		NodeIP:   "198.51.100.10",
		Hostname: "amsterdam.example.com",
		Remark:   "Amsterdam · CDN",
	}

	changed, err := server.upsertCDNHostRow(ctx, defaultCDNInboundTag, template, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the first pass should have created the host")
	}

	// Running again must not add a second row: two hosts for one node would
	// reach every user as a duplicate config.
	changed, err = server.upsertCDNHostRow(ctx, defaultCDNInboundTag, template, plan)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("the second pass should have found nothing to change")
	}
	var rows int
	if err := server.db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, defaultCDNInboundTag).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("hosts holds %d rows, want 1", rows)
	}

	var sni, host, remark sql.NullString
	if err := server.db.QueryRow(`SELECT sni, host, remark FROM hosts WHERE inbound_tag = ?`, defaultCDNInboundTag).
		Scan(&sni, &host, &remark); err != nil {
		t.Fatal(err)
	}
	if sni.String != plan.Hostname || host.String != plan.Hostname {
		t.Fatalf("sni=%q host=%q, both should be %q", sni.String, host.String, plan.Hostname)
	}
	if remark.String != plan.Remark {
		t.Fatalf("remark = %q, want %q", remark.String, plan.Remark)
	}

	// A hostname that drifted -- an SNI left pointing at the previous node --
	// is exactly what this is meant to repair.
	if _, err := server.db.Exec(`UPDATE hosts SET sni = ? WHERE inbound_tag = ?`, "stale.example.com", defaultCDNInboundTag); err != nil {
		t.Fatal(err)
	}
	changed, err = server.upsertCDNHostRow(ctx, defaultCDNInboundTag, template, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a drifted SNI should have been corrected")
	}
	if err := server.db.QueryRow(`SELECT sni FROM hosts WHERE inbound_tag = ?`, defaultCDNInboundTag).Scan(&sni); err != nil {
		t.Fatal(err)
	}
	if sni.String != plan.Hostname {
		t.Fatalf("sni = %q, want %q", sni.String, plan.Hostname)
	}
}

func newCDNAutomationTestServer(t *testing.T) *Server {
	t.Helper()
	testDir := t.TempDir()
	server, err := New(Config{
		Database:                    "sqlite:///" + filepath.ToSlash(filepath.Join(testDir, "cdn.sqlite3")),
		CertificateBase:             filepath.Join(testDir, "certificates"),
		ExternalAppsBase:            filepath.Join(testDir, "apps"),
		JWTAccessTokenExpireMinutes: 1440,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.db.Close() })
	return server
}

// Two syncs at once would both read "this host is missing" and both write it,
// which reaches every user as a duplicate config. A burst of node edits is
// exactly how that happens, so a kick while one is running has to fold into a
// single follow-up pass rather than start a second sync.
func TestKickCDNAutomationCoalescesWhileOneIsRunning(t *testing.T) {
	server := newCDNAutomationTestServer(t)

	server.cdnSyncStateMu.Lock()
	server.cdnSyncRunning = true
	server.cdnSyncStateMu.Unlock()

	for i := 0; i < 5; i++ {
		server.kickCDNAutomation("node created")
	}

	server.cdnSyncStateMu.Lock()
	running, pending := server.cdnSyncRunning, server.cdnSyncPending
	server.cdnSyncStateMu.Unlock()
	if !running {
		t.Fatal("the running sync should still be marked as running")
	}
	if !pending {
		t.Fatal("five kicks during a sync should have left exactly one pass pending")
	}
}

func TestKickCDNAutomationStartsWhenIdle(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	// Not configured, so the pass returns immediately; what is under test is
	// that the kick claims the slot rather than skipping.
	server.kickCDNAutomation("node created")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		server.cdnSyncStateMu.Lock()
		running := server.cdnSyncRunning
		server.cdnSyncStateMu.Unlock()
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the sync slot was never released")
}

// Adding a second CDN inbound without teaching the automation about it gave
// that inbound the one default host instead of a hostname per node, so its
// configs pointed at the bare domain and reached nothing.
func TestCDNAutomationCoversEveryCDNInbound(t *testing.T) {
	tags := cdnAutomation{}.inboundTags()
	if len(tags) < 2 {
		t.Fatalf("expected every CDN inbound to be covered, got %v", tags)
	}
	found := map[string]bool{}
	for _, tag := range tags {
		found[tag] = true
	}
	for _, want := range []string{"auto-cdn-xhttp", "auto-cdn-httpupgrade"} {
		if !found[want] {
			t.Fatalf("%s is not covered: %v", want, tags)
		}
	}
}

// An admin who named one tag meant that one. Widening it would start writing
// host rows on an inbound they deliberately left alone.
func TestCDNAutomationHonoursAnExplicitInboundTag(t *testing.T) {
	tags := cdnAutomation{InboundTag: " my-own-cdn "}.inboundTags()
	if len(tags) != 1 || tags[0] != "my-own-cdn" {
		t.Fatalf("inboundTags() = %v, want just the configured one", tags)
	}
}

// One hostname, one DNS record, but a host row on each inbound: the record is
// the same either way while each inbound has its own port and path.
func TestUpsertCDNHostRowWritesOneRowPerInbound(t *testing.T) {
	server := newCDNAutomationTestServer(t)
	ctx := context.Background()
	for _, tag := range []string{"auto-cdn-xhttp", "auto-cdn-httpupgrade"} {
		if _, err := server.db.Exec(`INSERT INTO inbounds (tag) VALUES (?)`, tag); err != nil {
			t.Fatal(err)
		}
	}
	plan := cdnHostPlan{
		NodeID:   1,
		NodeName: "Amsterdam",
		NodeIP:   "198.51.100.10",
		Hostname: "amsterdam.example.com",
		Remark:   "🇳🇱 Amsterdam · CDN",
	}
	for _, tag := range []string{"auto-cdn-xhttp", "auto-cdn-httpupgrade"} {
		port := int64(2096)
		path := "/27522950f26c"
		if tag == "auto-cdn-httpupgrade" {
			port, path = 2087, "/9357c9ae3a41"
		}
		template := hostPayload{Port: &port, Path: &path, Security: "tls"}
		changed, err := server.upsertCDNHostRow(ctx, tag, template, plan)
		if err != nil {
			t.Fatal(err)
		}
		if !changed {
			t.Fatalf("%s: expected the host to be created", tag)
		}
	}

	var rows int
	if err := server.db.QueryRow(
		`SELECT COUNT(*) FROM hosts WHERE LOWER(COALESCE(address, '')) = ?`,
		plan.Hostname).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("one hostname should appear once per inbound, got %d rows", rows)
	}
	// Each keeps its own inbound's port, or both configs would dial the same
	// one and only one of them could work.
	var ports []int64
	results, err := server.db.Query(`SELECT port FROM hosts WHERE LOWER(COALESCE(address, '')) = ? ORDER BY port`, plan.Hostname)
	if err != nil {
		t.Fatal(err)
	}
	defer results.Close()
	for results.Next() {
		var port int64
		if err := results.Scan(&port); err != nil {
			t.Fatal(err)
		}
		ports = append(ports, port)
	}
	if len(ports) != 2 || ports[0] != 2087 || ports[1] != 2096 {
		t.Fatalf("ports = %v, want [2087 2096]", ports)
	}
}
