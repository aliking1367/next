//go:build cgo

package xrayconfig

import (
	"context"
	"strings"
	"testing"
)

func TestApplyTSinHTTPConfigsOnTheFrontAddresses(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1", HTTP: true, Vision: true},
		{Port: 1095, Name: "Turkey", Code: "TR", Address: "10.0.0.1"},
	}
	gate := TSinGate{Host: "g1.example.com", DirectAddress: "167.233.180.133,167.233.184.49"}
	applied, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	var httpPort int
	for _, entry := range applied {
		if entry.HTTP {
			httpPort = entry.Port
		}
	}
	if httpPort == 0 {
		t.Fatalf("no HTTP config applied: %#v", applied)
	}
	inbound, err := repo.GetInbound(ctx, "tsin-germany-http")
	if err != nil {
		t.Fatalf("http inbound: %v", err)
	}
	if stringValue(inbound["protocol"]) != "vmess" || tsinHTTPInboundHost(inbound) != DefaultTSinHTTPHost {
		t.Fatalf("inbound = %#v", inbound)
	}
	if _, err := repo.GetInbound(ctx, "tsin-turkey-http"); err == nil {
		t.Fatal("Turkey was not chosen but got an HTTP config")
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany-vision"); err != nil {
		t.Fatalf("the Vision config must stay beside the HTTP one: %v", err)
	}

	var address, remark string
	if err := db.QueryRow(`SELECT address, remark FROM hosts WHERE inbound_tag = 'tsin-germany-http'`).Scan(&address, &remark); err != nil {
		t.Fatal(err)
	}
	if address != "167.233.180.133,167.233.184.49" || !strings.HasSuffix(remark, "Germany | همه اپراتورها") {
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
			if stringValue(tag) == "tsin-germany-http" {
				routed = true
			}
		}
	}
	if !routed {
		t.Fatal("the HTTP config is not routed to its country's exit")
	}
	for _, item := range listOfMaps(config["inbounds"]) {
		if stringValue(item["tag"]) != TSinGateTag {
			continue
		}
		for _, fallback := range listOfMaps(mapValue(item["settings"])["fallbacks"]) {
			if port, _ := parseConfigPort(fallback["dest"]); port == httpPort {
				t.Fatal("the gate must not fall back to the HTTP inbound")
			}
		}
	}

	// Re-applying keeps the port; the addresses-only save moves the HTTP row too.
	again, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range again {
		if entry.HTTP && (entry.Port != httpPort || entry.Created) {
			t.Fatalf("re-apply rebuilt the HTTP config: %#v", entry)
		}
	}
	direct := "167.233.176.171"
	if _, err := repo.SetTSinAddresses(ctx, &direct, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT address FROM hosts WHERE inbound_tag = 'tsin-germany-http'`).Scan(&address); err != nil {
		t.Fatal(err)
	}
	if address != direct {
		t.Fatalf("addresses save left the HTTP config on %q", address)
	}

	// Dropping the country's HTTP config removes its inbound.
	countries[0].HTTP = false
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany-http"); err == nil {
		t.Fatal("the HTTP inbound outlived its country's choice")
	}
}
