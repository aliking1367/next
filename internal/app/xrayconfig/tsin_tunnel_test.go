//go:build cgo

package xrayconfig

import (
	"context"
	"strings"
	"testing"
)

func TestTSinTunnelCountriesShareTunnelPortsByPath(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	gate := TSinGate{Host: "g1.example.com", TunnelHost: "tun.example.ir"}
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", TunnelPort: 20100},
		{Port: 1083, Name: "Netherlands", Code: "NL", TunnelPort: 20100},
		{Port: 1084, Name: "Sweden", Code: "SE", TunnelPort: 20101, TunnelAddress: "seller2.example.ir", TunnelRemotePort: 41000},
		{Port: 1090, Name: "Best ping"},
	}
	result, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	tunnels := 0
	for _, entry := range result {
		if entry.Tunnel {
			tunnels++
		}
	}
	if tunnels != 3 {
		t.Fatalf("got %d tunnel inbounds, want 3: %+v", tunnels, result)
	}

	german, err := repo.GetInbound(ctx, TSinTunnelInboundTag("Germany"))
	if err != nil {
		t.Fatal(err)
	}
	stream := mapValue(german["streamSettings"])
	if german["protocol"] != "vmess" || stream["network"] != "ws" || german["listen"] != "127.0.0.1" || tsinTunnelInboundPath(german) == "" {
		t.Fatalf("tunnel inbound %#v", german)
	}
	germanPort, _ := parseConfigPort(german["port"])

	// One gate per tunnel port, falling back by path to its countries only.
	gate20100, err := repo.GetInbound(ctx, tsinTunnelGateTag(20100))
	if err != nil {
		t.Fatal(err)
	}
	fallbacks := interfaceSlice(mapValue(gate20100["settings"])["fallbacks"])
	if len(fallbacks) != 2 {
		t.Fatalf("port 20100 has %d fallbacks, want 2", len(fallbacks))
	}
	found := false
	for _, item := range fallbacks {
		entry := mapValue(item)
		if dest, _ := parseConfigPort(entry["dest"]); dest == germanPort && entry["path"] == tsinTunnelInboundPath(german) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Germany is not behind port 20100: %#v", fallbacks)
	}
	if gate, err := repo.GetInbound(ctx, tsinTunnelGateTag(20101)); err != nil || len(interfaceSlice(mapValue(gate["settings"])["fallbacks"])) != 1 {
		t.Fatalf("port 20101 gate: %v", err)
	}
	var gateHosts int
	_ = repo.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM hosts WHERE inbound_tag LIKE ?`, TSinTunnelGatePrefix+"%").Scan(&gateHosts)
	if gateHosts != 0 {
		t.Fatal("a tunnel gate got a config of its own")
	}

	// The country's tunnel config points at the tunnel on its port, without TLS.
	var remark, address, security string
	var port int
	if err := repo.db.QueryRowContext(ctx, `SELECT remark, address, port, security FROM hosts WHERE inbound_tag = ?`, TSinTunnelInboundTag("Sweden")).
		Scan(&remark, &address, &port, &security); err != nil {
		t.Fatal(err)
	}
	// Sweden's port has its own seller address and tunnel-side port.
	if !strings.HasSuffix(remark, tsinTunnelRemark) || address != "seller2.example.ir" || port != 41000 || security != "inbound_default" {
		t.Fatalf("Sweden tunnel host: %q %q %d %q", remark, address, port, security)
	}
	// Germany's port has none, so it uses the shared address and its own number.
	if err := repo.db.QueryRowContext(ctx, `SELECT address, port FROM hosts WHERE inbound_tag = ?`, TSinTunnelInboundTag("Germany")).
		Scan(&address, &port); err != nil || address != "tun.example.ir" || port != 20100 {
		t.Fatalf("Germany tunnel host: %q %d %v", address, port, err)
	}

	// The Cloudflare gate does not take the tunnel inbounds, and the tunnel
	// inbounds leave through their country's exit.
	cf, _ := repo.GetInbound(ctx, TSinGateTag)
	for _, item := range interfaceSlice(mapValue(cf["settings"])["fallbacks"]) {
		if dest, _ := parseConfigPort(mapValue(item)["dest"]); dest == germanPort {
			t.Fatal("the Cloudflare gate falls back to a tunnel inbound")
		}
	}
	config, _ := repo.GetTargetRawConfig(ctx, "master")
	routed := false
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if stringValue(rule["outboundTag"]) != TSinOutboundTag(1082) {
			continue
		}
		for _, tag := range interfaceSlice(rule["inboundTag"]) {
			if tag == TSinTunnelInboundTag("Germany") {
				routed = true
			}
		}
	}
	if !routed {
		t.Fatal("Germany's tunnel inbound does not leave through Germany")
	}

	// Applying again keeps the tunnel inbound's port and path.
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	again, _ := repo.GetInbound(ctx, TSinTunnelInboundTag("Germany"))
	if p, _ := parseConfigPort(again["port"]); p != germanPort || tsinTunnelInboundPath(again) != tsinTunnelInboundPath(german) {
		t.Fatal("a second apply changed the tunnel inbound")
	}

	// Dropping the tunnel removes its inbounds and gates.
	for i := range countries {
		countries[i].TunnelPort = 0
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	tags, _ := repo.CollectInboundTags(ctx)
	for tag := range tags {
		if strings.HasSuffix(tag, TSinTunnelSuffix) || strings.HasPrefix(tag, TSinTunnelGatePrefix) {
			t.Fatalf("%s survived dropping the tunnel", tag)
		}
	}
}
