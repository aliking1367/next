package xrayconfig

import (
	"context"
	"strings"
	"testing"
)

func nordTestOutbound(tag string) map[string]any {
	return map[string]any{"tag": tag, "protocol": "wireguard", "settings": map[string]any{
		"secretKey": "k", "address": []any{"10.5.0.2/32"},
		"peers": []any{map[string]any{"publicKey": "p", "endpoint": "1.2.3.4:51820"}},
	}}
}

func TestSyncNordAutoAdoptsBuildsAndRemovesLocations(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()

	// A panel as it is today: a T.Sin country, a hand-made NordVPN config for
	// Turkey with its rules, and a second location with no config yet.
	for _, tag := range []string{"tsin-turkey", "gaming-tr"} {
		inbound, err := buildTSinVisionInbound(tag, map[string]int{"tsin-turkey": 22577, "gaming-tr": 23560}[tag], DefaultTSinVisionSNI)
		if err != nil {
			t.Fatal(err)
		}
		inbound["targets"] = []any{"master"}
		if _, err := repo.CreateInbound(ctx, inbound); err != nil {
			t.Fatalf("create %s: %v", tag, err)
		}
	}
	config, err := repo.GetTargetRawConfig(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	config["outbounds"] = []any{
		map[string]any{"tag": "DIRECT", "protocol": "freedom"},
		map[string]any{"tag": "BLOCK", "protocol": "blackhole"},
		map[string]any{"tag": "tsin-out-1095", "protocol": "socks", "settings": map[string]any{"servers": []any{map[string]any{"address": "10.0.0.1", "port": 1095}}}},
		nordTestOutbound("nord-tr80.nordvpn.com"),
		nordTestOutbound("nord-ae72.nordvpn.com"),
	}
	config["routing"] = map[string]any{"rules": []any{
		map[string]any{"type": "field", "ip": []any{"geoip:private"}, "outboundTag": "BLOCK"},
		map[string]any{"type": "field", "inboundTag": []any{"tsin-turkey"}, "outboundTag": "tsin-out-1095"},
		map[string]any{"type": "field", "inboundTag": []any{"gaming-tr"}, "domain": []any{"domain:openai.com"}, "outboundTag": "nord-tr80.nordvpn.com"},
		map[string]any{"type": "field", "inboundTag": []any{"gaming-tr"}, "outboundTag": "nord-tr80.nordvpn.com"},
	}}
	if _, err := repo.SaveTargetRawConfig(ctx, "master", config); err != nil {
		t.Fatalf("save: %v", err)
	}

	opts := NordAutoOptions{Target: "master", Address: "195.201.252.8", AIOutbound: "nord-ae72.nordvpn.com"}
	result, err := repo.SyncNordAuto(ctx, opts)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.Managed["nord-tr80.nordvpn.com"] != "gaming-tr" {
		t.Fatalf("the hand-made Turkey config was not adopted: %#v", result.Managed)
	}
	if got := result.Managed["nord-ae72.nordvpn.com"]; got != "nord-in-ae72" || len(result.Created) != 1 {
		t.Fatalf("UAE config = %q, created %v", got, result.Created)
	}
	var remark, address string
	if err := db.QueryRow(`SELECT remark, address FROM hosts WHERE inbound_tag = 'nord-in-ae72'`).Scan(&remark, &address); err != nil {
		t.Fatal(err)
	}
	if remark != "🇦🇪 UAE" || address != "195.201.252.8" {
		t.Fatalf("host = %q %q", remark, address)
	}

	config, err = repo.GetTargetRawConfig(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	rules := interfaceSlice(mapValue(config["routing"])["rules"])
	var summary []string
	for _, item := range rules {
		rule := mapValue(item)
		summary = append(summary, firstNonEmptyString(stringValue(rule["ruleTag"]), "-")+">"+stringValue(rule["outboundTag"]))
	}
	want := []string{
		"->BLOCK", "->tsin-out-1095",
		"nord-auto-ai>nord-ae72.nordvpn.com",
		"nord-auto-gaming-tr>nord-tr80.nordvpn.com",
		"nord-auto-nord-in-ae72>nord-ae72.nordvpn.com",
	}
	if strings.Join(summary, " ") != strings.Join(want, " ") {
		t.Fatalf("rules = %v", summary)
	}
	ai := mapValue(rules[2])
	if got := interfaceSlice(ai["inboundTag"]); len(got) != 2 || len(interfaceSlice(ai["domain"])) != len(NordAutoAIDomains) {
		t.Fatalf("AI rule = %#v", ai)
	}

	// Nothing new: nothing saved.
	opts.Managed = result.Managed
	again, err := repo.SyncNordAuto(ctx, opts)
	if err != nil || again.Changed {
		t.Fatalf("second sync changed=%v err=%v", again.Changed, err)
	}

	// A location removed in the NordVPN dialog loses its config.
	config["outbounds"] = interfaceSlice(config["outbounds"])[:4]
	if _, err := repo.SaveTargetRawConfig(ctx, "master", config); err != nil {
		t.Fatal(err)
	}
	opts.Managed = again.Managed
	opts.AIOutbound = "nord-ae72.nordvpn.com" // now gone: no AI rule
	removed, err := repo.SyncNordAuto(ctx, opts)
	if err != nil {
		t.Fatalf("sync after removal: %v", err)
	}
	if len(removed.Removed) != 1 || removed.Removed[0] != "nord-in-ae72" {
		t.Fatalf("removed = %v", removed.Removed)
	}
	if _, err := repo.GetInbound(ctx, "nord-in-ae72"); err == nil {
		t.Fatal("the UAE inbound is still there")
	}
	config, _ = repo.GetTargetRawConfig(ctx, "master")
	for _, item := range interfaceSlice(mapValue(config["routing"])["rules"]) {
		rule := mapValue(item)
		if strings.Contains(stringValue(rule["outboundTag"]), "ae72") {
			t.Fatalf("a rule still points at the removed location: %#v", rule)
		}
	}
	if _, err := repo.GetInbound(ctx, "tsin-turkey"); err != nil {
		t.Fatalf("the T.Sin country was touched: %v", err)
	}
}

func TestNordLocationNames(t *testing.T) {
	for tag, want := range map[string]string{
		"nord-tr80.nordvpn.com":   "🇹🇷 Turkey",
		"nord-us9150.nordvpn.com": "🇺🇸 USA",
		"nord-uk2001.nordvpn.com": "🇬🇧 UK",
		"nord-iq3.nordvpn.com":    "🇮🇶 Iraq",
		"nord-zz1.nordvpn.com":    "ZZ",
	} {
		if got := NordLocationRemark(NordLocationCode(tag)); got != want {
			t.Errorf("%s = %q, want %q", tag, got, want)
		}
	}
	if got := nordAutoInboundTag("nord-us9150.nordvpn.com", map[string]bool{"nord-in-us9150": true}); got != "nord-in-us9150-2" {
		t.Errorf("tag = %q", got)
	}
}
