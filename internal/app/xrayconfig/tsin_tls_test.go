//go:build cgo

package xrayconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTSinTLSCert(t *testing.T, serverName string) {
	t.Helper()
	certFile, keyFile := TSinTLSCertificatePaths(serverName)
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{certFile, keyFile} {
		if err := os.WriteFile(file, []byte("-----BEGIN TEST-----\nx\n-----END TEST-----\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApplyTSinTLSConfigs(t *testing.T) {
	previous := TSinTLSCertDir
	TSinTLSCertDir = t.TempDir()
	t.Cleanup(func() { TSinTLSCertDir = previous })
	const name1 = "static.176.221.75.5.clients.your-server.de"
	const name2 = "static.144.222.75.5.clients.your-server.de"
	writeTSinTLSCert(t, name1)
	writeTSinTLSCert(t, name2)

	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1", TLS: true, HTTP: true},
		{Port: 1090, Name: "Best Ping", Code: "", Address: "10.0.0.1", TLS: true},
		{Port: 1095, Name: "Turkey", Code: "TR", Address: "10.0.0.1"},
	}
	gate := TSinGate{Host: "g1.example.com", DirectAddress: "static.example.com", TLSServerName: name1, TLSMainCountry: "Best Ping"}
	applied, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// One shared inbound on 443, owned by the chosen country.
	owner := "tsin-best-ping-tls"
	tlsEntries := 0
	for _, entry := range applied {
		if entry.TLS {
			tlsEntries++
			if entry.Tag != owner || entry.Port != 443 {
				t.Fatalf("TLS entry = %#v", entry)
			}
		}
	}
	if tlsEntries != 1 {
		t.Fatalf("%d TLS inbounds, want one shared", tlsEntries)
	}
	inbound, err := repo.GetInbound(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(inbound["protocol"]) != "vless" || tsinTLSInboundServerName(inbound) != name1 {
		t.Fatalf("inbound = %#v", inbound)
	}
	for _, tag := range []string{"tsin-germany-tls", "tsin-turkey-tls"} {
		if _, err := repo.GetInbound(ctx, tag); err == nil {
			t.Fatalf("%s has an inbound of its own", tag)
		}
	}

	// The owner's host has no route; Germany's host sits on the same
	// inbound with its port as the route number.
	hostOf := func(route int) (string, string, string, string) {
		t.Helper()
		var address, sni, fingerprint, remark string
		if err := db.QueryRow(`SELECT address, COALESCE(sni,''), COALESCE(fingerprint,''), remark FROM hosts WHERE inbound_tag = ? AND COALESCE(vless_route,0) = ?`, owner, route).Scan(&address, &sni, &fingerprint, &remark); err != nil {
			t.Fatalf("host with route %d: %v", route, err)
		}
		return address, sni, fingerprint, remark
	}
	address, sni, fingerprint, remark := hostOf(1082)
	if address != name1 || sni != name1 || fingerprint != "chrome" || !strings.Contains(remark, "Germany | TLS") {
		t.Fatalf("Germany host = %q %q %q %q", address, sni, fingerprint, remark)
	}
	if _, _, _, remark = hostOf(0); !strings.Contains(remark, "Best Ping | TLS") {
		t.Fatalf("owner host remark = %q", remark)
	}

	config, err := repo.MasterRawConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules := listOfMaps(mapValue(config["routing"])["rules"])
	routeAt, ownerAt := -1, -1
	for i, rule := range rules {
		tags := interfaceSlice(rule["inboundTag"])
		if len(tags) == 0 {
			continue
		}
		if stringValue(rule["vlessRoute"]) == "1082" && stringValue(rule["outboundTag"]) == "tsin-out-1082" && stringValue(tags[0]) == owner {
			routeAt = i
		}
		for _, tag := range tags {
			if stringValue(tag) == owner && rule["vlessRoute"] == nil && stringValue(rule["outboundTag"]) == "tsin-out-1090" {
				ownerAt = i
			}
			if stringValue(tag) == "tsin-germany-tls" {
				t.Fatal("a rule still names Germany's own TLS inbound")
			}
		}
	}
	if routeAt < 0 || ownerAt < 0 || routeAt > ownerAt {
		t.Fatalf("route rule at %d, owner rule at %d: %#v", routeAt, ownerAt, rules)
	}

	// The addresses-only save leaves the TLS rows on their own name.
	direct := "167.233.176.171"
	if _, err := repo.SetTSinAddresses(ctx, &direct, nil); err != nil {
		t.Fatal(err)
	}
	if address, _, _, _ = hostOf(1082); address != name1 {
		t.Fatalf("addresses save moved the TLS config to %q", address)
	}

	// A new name (new IP) keeps the inbound and swaps the certificate.
	gate.TLSServerName = name2
	again, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range again {
		if entry.TLS && (entry.Port != 443 || entry.Created) {
			t.Fatalf("new name rebuilt the TLS config: %#v", entry)
		}
	}
	inbound, _ = repo.GetInbound(ctx, owner)
	if tsinTLSInboundServerName(inbound) != name2 {
		t.Fatalf("server name not swapped: %#v", inbound)
	}
	for _, route := range []int{0, 1082} {
		if address, sni, _, _ = hostOf(route); address != name2 || sni != name2 {
			t.Fatalf("host %d not moved to the new name: %q %q", route, address, sni)
		}
	}
	var hosts int
	_ = db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, owner).Scan(&hosts)
	if hosts != 2 {
		t.Fatalf("%d hosts on the shared inbound after a re-apply, want 2", hosts)
	}

	// Dropping Germany's TLS config removes its host and route.
	countries[0].TLS = false
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE inbound_tag = ? AND COALESCE(vless_route,0) <> 0`, owner).Scan(&hosts)
	if hosts != 0 {
		t.Fatalf("Germany's TLS host outlived its choice")
	}
	config, _ = repo.MasterRawConfig(ctx)
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if rule["vlessRoute"] != nil {
			t.Fatalf("route rule outlived its country: %#v", rule)
		}
	}

	// Dropping every TLS country removes the shared inbound.
	countries[1].TLS = false
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, owner); err == nil {
		t.Fatal("the TLS inbound outlived its countries")
	}
}

func TestAssignTSinTLSRoutes(t *testing.T) {
	countries := []TSinCountry{{Name: "Germany", Port: 1082, TLS: true}, {Name: "Spain", Port: 1090, TLS: true}, {Name: "UK", Port: 1084}}
	out, err := assignTSinTLSRoutes(countries, "")
	if err != nil {
		t.Fatal(err)
	}
	if out[0].TLSRoute != 0 || out[1].TLSRoute != 1090 || out[2].TLSRoute != 0 {
		t.Fatalf("no main chosen: %#v", out)
	}
	if out, _ = assignTSinTLSRoutes(countries, "spain"); out[0].TLSRoute != 1082 || out[1].TLSRoute != 0 {
		t.Fatalf("Spain as main: %#v", out)
	}
	if countries[0].TLSRoute != 0 || countries[1].TLSRoute != 0 {
		t.Fatal("the caller's countries were changed")
	}
	if _, err := assignTSinTLSRoutes([]TSinCountry{{Name: "A", Port: 1, TLS: true}, {Name: "B", Port: 20000, TLS: true}}, "A"); err == nil {
		t.Fatal("a route number inside the random UUIDs' range was accepted")
	}
}

func TestNormalizeTSinTLSServerName(t *testing.T) {
	if got, err := NormalizeTSinTLSServerName(" Static.1.2.3.4.Clients.Your-Server.de "); err != nil || got != "static.1.2.3.4.clients.your-server.de" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"1.2.3.4", "https://x.example.com", "x.example.com:443"} {
		if _, err := NormalizeTSinTLSServerName(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
