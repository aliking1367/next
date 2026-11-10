//go:build cgo

package xrayconfig

import (
	"context"
	"strings"
	"testing"
)

// In path mode a country can also get a Fastly config: XHTTP in packet-up
// mode over TLS on its own port, routed to the same exit, outside the gate,
// with its host pointing at the Fastly domain.
func TestApplyTSinAddsFastlyConfigsOnTheirOwnPorts(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Fastly: true},
		{Port: 1095, Name: "Best ping", Fastly: true, Direct: true},
		{Port: 1096, Name: "Turkey", Code: "TR"},
	}
	gate := TSinGate{Host: "g1.example.com", FastlyHost: "Next.Global.SSL.Fastly.net"}
	applied, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 6 {
		t.Fatalf("applied = %#v, want three countries, one direct and two Fastly configs", applied)
	}
	ports := map[int]bool{}
	for _, name := range []string{"Germany", "Best ping"} {
		inbound, err := repo.GetInbound(ctx, TSinFastlyInboundTag(name))
		if err != nil {
			t.Fatalf("%s Fastly inbound: %v", name, err)
		}
		stream := mapValue(inbound["streamSettings"])
		xhttp := mapValue(stream["xhttpSettings"])
		if stringValue(stream["network"]) != "xhttp" || stringValue(stream["security"]) != "tls" || stringValue(xhttp["mode"]) != "packet-up" {
			t.Fatalf("%s Fastly inbound is not TLS XHTTP packet-up: %#v", name, stream)
		}
		if stringValue(inbound["listen"]) != "::" {
			t.Fatalf("%s Fastly inbound must listen publicly for Fastly: %v", name, inbound["listen"])
		}
		port, _ := parseConfigPort(inbound["port"])
		if ports[port] {
			t.Fatalf("two Fastly inbounds share port %d", port)
		}
		ports[port] = true
		var remark, address, sni, host, security string
		if err := db.QueryRow(`SELECT remark, address, COALESCE(sni, ''), COALESCE(host, ''), security FROM hosts WHERE inbound_tag = ?`,
			TSinFastlyInboundTag(name)).Scan(&remark, &address, &sni, &host, &security); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(remark, "🅵") || address != "next.global.ssl.fastly.net" || sni != address || host != address || security != "tls" {
			t.Fatalf("%s Fastly host = %q %q %q %q %q", name, remark, address, sni, host, security)
		}
	}
	if _, err := repo.GetInbound(ctx, TSinFastlyInboundTag("Turkey")); err == nil {
		t.Fatal("Turkey got a Fastly config it was not given")
	}
	gateInbound, err := repo.GetInbound(ctx, TSinGateTag)
	if err != nil {
		t.Fatal(err)
	}
	if fallbacks := listOfMaps(mapValue(gateInbound["settings"])["fallbacks"]); len(fallbacks) != 3 {
		t.Fatalf("the gate must only carry the three path configs: %#v", fallbacks)
	}
	config, err := repo.GetTargetRawConfig(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	routed := false
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if stringValue(rule["outboundTag"]) != TSinOutboundTag(1082) {
			continue
		}
		for _, tag := range interfaceSlice(rule["inboundTag"]) {
			if tag == TSinFastlyInboundTag("Germany") {
				routed = true
			}
		}
	}
	if !routed {
		t.Fatal("the Fastly config is not routed to Germany's exit")
	}
	backends := repo.TSinFastlyBackends(ctx, []string{"Germany", "Best ping", "Turkey"})
	if len(backends) != 2 || backends[0].Path == "" || backends[0].Port == 0 {
		t.Fatalf("backends = %#v", backends)
	}

	// Applying again keeps the ports and paths, so Fastly's backends stay right.
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	again := repo.TSinFastlyBackends(ctx, []string{"Germany", "Best ping"})
	if again[0] != backends[0] || again[1] != backends[1] {
		t.Fatalf("a second apply moved the Fastly backends: %#v -> %#v", backends, again)
	}

	// Moving the Cloudflare domain leaves the Fastly configs alone.
	if _, err := repo.SetTSinGateHost(ctx, "g1.example.com", "g2.example.com"); err != nil {
		t.Fatal(err)
	}
	var address string
	if err := db.QueryRow(`SELECT address FROM hosts WHERE inbound_tag = ?`, TSinFastlyInboundTag("Germany")).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if address != "next.global.ssl.fastly.net" {
		t.Fatalf("the Cloudflare domain swap moved a Fastly config to %q", address)
	}

	// No Fastly domain: the Fastly configs go.
	gate.FastlyHost = ""
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinFastlyInboundTag("Germany")); err == nil {
		t.Fatal("the Fastly config survived the Fastly domain being cleared")
	}
}
