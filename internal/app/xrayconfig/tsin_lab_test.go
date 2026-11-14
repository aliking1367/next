//go:build cgo

package xrayconfig

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestTSinLabMakesTestInboundsThroughTheBestPingExitAndRemovesThem(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	if _, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", "g1.example.com", nil); err == nil {
		t.Fatal("the lab must refuse to start before the countries are applied")
	}
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE"},
		{Port: 1090, Name: "Best ping"},
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	tests, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", "g1.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tests) != 9 {
		t.Fatalf("got %d lab tests, want 9", len(tests))
	}
	// The tunnel test sits on its fixed port, VMess over WebSocket, and links
	// to the server itself, then once more through the tunnel host.
	withTunnel, err := repo.TSinLabTests(ctx, "master", "203.0.113.9", "g1.example.com", "198.51.100.7", "tun.example.ir")
	if err != nil || len(withTunnel) != 10 {
		t.Fatalf("with the tunnel: %d tests, %v", len(withTunnel), err)
	}
	var tunnelLinks []string
	for _, test := range withTunnel {
		if strings.HasPrefix(test.Tag, TSinLabPrefix+"raw-vmess") {
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(test.Link, "vmess://"))
			if test.Port != TSinLabRawVMessPort || !strings.Contains(string(raw), `"net":"tcp"`) || !strings.Contains(string(raw), `"add":"198.51.100.7"`) {
				t.Fatalf("raw VMess test %d %s", test.Port, raw)
			}
		}
		if strings.HasPrefix(test.Tag, TSinLabPrefix+"tunnel-vmess") {
			if test.Port != TSinLabTunnelPort {
				t.Fatalf("tunnel test on port %d", test.Port)
			}
			raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(test.Link, "vmess://"))
			tunnelLinks = append(tunnelLinks, string(raw))
		}
	}
	if len(tunnelLinks) != 2 || !strings.Contains(tunnelLinks[0], `"add":"198.51.100.7"`) || !strings.Contains(tunnelLinks[1], `"add":"tun.example.ir"`) ||
		!strings.Contains(tunnelLinks[0], `"net":"ws"`) || !strings.Contains(tunnelLinks[0], `"port":"20100"`) {
		t.Fatalf("tunnel links %v", tunnelLinks)
	}
	for _, test := range tests {
		if strings.HasPrefix(test.Link, "vmess://") {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(test.Link, "vmess://"))
			if err != nil || !strings.Contains(string(raw), `"add":"203.0.113.9"`) {
				t.Fatalf("lab VMess link %q: %v", raw, err)
			}
			continue
		}
		if strings.Contains(test.Link, "@g1.example.com:443") {
			continue
		}
		if !strings.HasPrefix(test.Link, "vless://") || !strings.Contains(test.Link, "@203.0.113.9:") {
			t.Fatalf("lab link %q", test.Link)
		}
	}
	config, err := repo.GetTargetRawConfig(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	rules := listOfMaps(mapValue(config["routing"])["rules"])
	if !tsinLabRule(rules[0]) || stringValue(rules[0]["outboundTag"]) != TSinOutboundTag(1090) {
		t.Fatalf("the lab must leave through Best ping's exit first: %#v", rules[0])
	}
	// A second create keeps the same lab and links.
	again, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", "g1.example.com", nil)
	if err != nil || again[0].Link != tests[0].Link {
		t.Fatalf("a second create made a new lab: %v", err)
	}
	// The Cloudflare check never takes a lab inbound for a country.
	if err := repo.RemoveTSinLab(ctx, "master"); err != nil {
		t.Fatal(err)
	}
	config, _ = repo.GetTargetRawConfig(ctx, "master")
	for _, inbound := range listOfMaps(config["inbounds"]) {
		if strings.HasPrefix(stringValue(inbound["tag"]), TSinLabPrefix) {
			t.Fatalf("lab inbound %v survived removal", inbound["tag"])
		}
	}
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if tsinLabRule(rule) {
			t.Fatal("the lab rule survived removal")
		}
	}

	// Applying the countries also removes a lab left behind.
	if _, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", "g1.example.com", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinLabPrefix+"http"); err == nil {
		t.Fatal("applying the countries kept the lab")
	}
}

// A making that stopped half way leaves lab inbounds without their test
// client; the next making starts over instead of failing on a duplicate tag,
// and a fixed-port test whose port is taken is left out, not the whole lab.
func TestTSinLabRecoversFromAHalfMadeLabAndSkipsABusyPort(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{{Port: 1090, Name: "Best ping"}}
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	leftover := map[string]any{
		"tag": TSinLabPrefix + "http", "listen": "::", "port": 30123, "protocol": "vless",
		"settings":       map[string]any{"clients": []any{}, "decryption": "none"},
		"streamSettings": map[string]any{"network": "tcp", "security": "none"},
		"targets":        []any{"master"},
	}
	if _, err := repo.CreateInbound(ctx, leftover); err != nil {
		t.Fatal(err)
	}
	busy := func(port int) bool { return port == TSinLabTunnelPort }
	tests, err := repo.EnsureTSinLab(ctx, "master", "203.0.113.9", "g1.example.com", busy)
	if err != nil {
		t.Fatalf("a half-made lab blocked a new one: %v", err)
	}
	for _, test := range tests {
		if strings.HasPrefix(test.Tag, TSinLabPrefix+"tunnel-vmess") {
			t.Fatal("the tunnel test was made on a busy port")
		}
	}
	if len(tests) != len(tsinLabRecipes())-1 {
		t.Fatalf("got %d tests, want all but the tunnel one", len(tests))
	}
}
