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
	ports := map[string]int{}
	for _, entry := range applied {
		if entry.TLS {
			ports[entry.Country] = entry.Port
		}
	}
	if ports["Best Ping"] != 443 || ports["Germany"] == 0 || ports["Germany"] == 443 {
		t.Fatalf("TLS ports = %#v", ports)
	}
	inbound, err := repo.GetInbound(ctx, "tsin-germany-tls")
	if err != nil {
		t.Fatal(err)
	}
	if stringValue(inbound["protocol"]) != "vless" || tsinTLSInboundServerName(inbound) != name1 {
		t.Fatalf("inbound = %#v", inbound)
	}
	if _, err := repo.GetInbound(ctx, "tsin-turkey-tls"); err == nil {
		t.Fatal("Turkey was not chosen but got a TLS config")
	}

	var address, sni, fingerprint, remark string
	if err := db.QueryRow(`SELECT address, COALESCE(sni,''), COALESCE(fingerprint,''), remark FROM hosts WHERE inbound_tag = 'tsin-germany-tls'`).Scan(&address, &sni, &fingerprint, &remark); err != nil {
		t.Fatal(err)
	}
	if address != name1 || sni != name1 || fingerprint != "chrome" || !strings.Contains(remark, "Germany | TLS") {
		t.Fatalf("host = %q %q %q %q", address, sni, fingerprint, remark)
	}

	config, err := repo.MasterRawConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	routed := false
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if stringValue(rule["outboundTag"]) != "tsin-out-1082" {
			continue
		}
		for _, tag := range interfaceSlice(rule["inboundTag"]) {
			if stringValue(tag) == "tsin-germany-tls" {
				routed = true
			}
		}
	}
	if !routed {
		t.Fatal("the TLS config is not routed to its country's exit")
	}

	// The addresses-only save leaves the TLS rows on their own name.
	direct := "167.233.176.171"
	if _, err := repo.SetTSinAddresses(ctx, &direct, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT address FROM hosts WHERE inbound_tag = 'tsin-germany-tls'`).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if address != name1 {
		t.Fatalf("addresses save moved the TLS config to %q", address)
	}

	// A new name (new IP) keeps the ports and swaps the certificate.
	gate.TLSServerName = name2
	again, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range again {
		if entry.TLS && (entry.Port != ports[entry.Country] || entry.Created) {
			t.Fatalf("new name rebuilt the TLS config: %#v", entry)
		}
	}
	inbound, _ = repo.GetInbound(ctx, "tsin-germany-tls")
	if tsinTLSInboundServerName(inbound) != name2 {
		t.Fatalf("server name not swapped: %#v", inbound)
	}
	if err := db.QueryRow(`SELECT address, sni FROM hosts WHERE inbound_tag = 'tsin-germany-tls'`).Scan(&address, &sni); err != nil {
		t.Fatal(err)
	}
	if address != name2 || sni != name2 {
		t.Fatalf("host not moved to the new name: %q %q", address, sni)
	}

	// Dropping a country's TLS config removes its inbound.
	countries[0].TLS = false
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany-tls"); err == nil {
		t.Fatal("the TLS inbound outlived its country's choice")
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
