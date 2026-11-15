//go:build cgo

package xrayconfig

import (
	"context"
	"testing"
)

func TestApplyTSinVisionConfigsBorrowTheChosenSiteBesideDirectOnes(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1", Vision: true, Direct: true},
		{Port: 1095, Name: "Turkey", Code: "TR", Address: "10.0.0.1"},
	}
	gate := TSinGate{Host: "g1.example.com", DirectAddress: "195.201.252.8"}
	applied, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var visionPort int
	for _, entry := range applied {
		if entry.Vision {
			visionPort = entry.Port
		}
	}
	if visionPort == 0 {
		t.Fatalf("no Vision config applied: %#v", applied)
	}
	inbound, err := repo.GetInbound(ctx, "tsin-germany-vision")
	if err != nil {
		t.Fatalf("vision inbound: %v", err)
	}
	if got := tsinVisionInboundSNI(inbound); got != DefaultTSinVisionSNI {
		t.Fatalf("sni = %q", got)
	}
	reality := mapValue(mapValue(inbound["streamSettings"])["realitySettings"])
	if stringValue(reality["dest"]) != "www.hetzner.com:443" || stringValue(reality["privateKey"]) == "" {
		t.Fatalf("reality = %#v", reality)
	}
	if _, err := repo.GetInbound(ctx, "tsin-turkey-vision"); err == nil {
		t.Fatal("Turkey was not chosen but got a Vision config")
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany-direct"); err != nil {
		t.Fatalf("the direct config must stay beside the Vision one: %v", err)
	}

	var address, remark string
	if err := db.QueryRow(`SELECT address, remark FROM hosts WHERE inbound_tag = 'tsin-germany-vision'`).Scan(&address, &remark); err != nil {
		t.Fatal(err)
	}
	if address != "195.201.252.8" || remark == "" {
		t.Fatalf("host = %q %q", address, remark)
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
			if stringValue(tag) == "tsin-germany-vision" {
				routed = true
			}
		}
	}
	if !routed {
		t.Fatal("the Vision config is not routed to its country's exit")
	}
	for _, item := range listOfMaps(config["inbounds"]) {
		if stringValue(item["tag"]) != TSinGateTag {
			continue
		}
		for _, fallback := range listOfMaps(mapValue(item["settings"])["fallbacks"]) {
			if port, _ := parseConfigPort(fallback["dest"]); port == visionPort {
				t.Fatal("the gate must not fall back to the Vision inbound")
			}
		}
	}

	// Re-applying keeps the link; a new site rebuilds it.
	again, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range again {
		if entry.Vision && (entry.Created || entry.Port != visionPort) {
			t.Fatalf("re-apply moved the Vision config: %#v", entry)
		}
	}
	gate.VisionSNI = "www.example.org"
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	inbound, err = repo.GetInbound(ctx, "tsin-germany-vision")
	if err != nil || tsinVisionInboundSNI(inbound) != "www.example.org" {
		t.Fatalf("site change not applied: %v %#v", err, inbound)
	}

	// Unticking the country retires it.
	countries[0].Vision = false
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany-vision"); err == nil {
		t.Fatal("unticked Vision config was left behind")
	}
}

func TestNormalizeTSinVisionSNI(t *testing.T) {
	if got, err := NormalizeTSinVisionSNI(""); err != nil || got != DefaultTSinVisionSNI {
		t.Fatalf("empty = %q %v", got, err)
	}
	if got, err := NormalizeTSinVisionSNI(" WWW.Hetzner.com "); err != nil || got != "www.hetzner.com" {
		t.Fatalf("mixed case = %q %v", got, err)
	}
	if _, err := NormalizeTSinVisionSNI("https://x.com/a"); err == nil {
		t.Fatal("a URL must be refused")
	}
}

func TestSetTSinRoutingKeepsBlockRulesAheadOfTheCountries(t *testing.T) {
	config := map[string]any{
		"outbounds": []any{
			map[string]any{"tag": "DIRECT", "protocol": "freedom"},
			map[string]any{"tag": "BLOCK", "protocol": "blackhole"},
			map[string]any{"tag": "tsin-out-1082", "protocol": "socks"},
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []any{"emergency-vmess"}, "outboundTag": "DIRECT"},
			map[string]any{"type": "field", "inboundTag": []any{"tsin-germany"}, "outboundTag": "tsin-out-1082"},
			map[string]any{"type": "field", "protocol": []any{"bittorrent"}, "outboundTag": "BLOCK"},
			map[string]any{"type": "field", "ip": []any{"geoip:private"}, "outboundTag": "BLOCK"},
		}},
	}
	setTSinRouting(config, []TSinCountry{{Port: 1082, Name: "Germany", Address: "10.0.0.1"}})
	rules := listOfMaps(mapValue(config["routing"])["rules"])
	got := make([]string, 0, len(rules))
	for _, rule := range rules {
		got = append(got, stringValue(rule["outboundTag"]))
	}
	want := []string{"BLOCK", "BLOCK", "tsin-out-1082", "DIRECT"}
	if len(got) != len(want) {
		t.Fatalf("rules = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rules = %v, want %v", got, want)
		}
	}
	// Applying again keeps the same order and does not duplicate anything.
	setTSinRouting(config, []TSinCountry{{Port: 1082, Name: "Germany", Address: "10.0.0.1"}})
	if again := listOfMaps(mapValue(config["routing"])["rules"]); len(again) != len(want) {
		t.Fatalf("re-apply changed the rules: %#v", again)
	}
}
