package xrayconfig

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestApplyTSinCloudXHTTPSplitsByName(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Cloud: true},
		{Port: 1095, Name: "Best ping", Cloud: true},
		{Port: 1096, Name: "Turkey", Code: "TR"},
	}
	gate := TSinGate{Host: "g1.example.com", CloudDomain: "X.Example.com", CloudConnectAddress: "render.com"}
	applied, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 5 {
		t.Fatalf("applied = %#v, want three countries and two Cloudflare XHTTP configs", applied)
	}
	ports := map[string]int{}
	for _, name := range []string{"Germany", "Best ping"} {
		tag := TSinCloudInboundTag(name)
		inbound, err := repo.GetInbound(ctx, tag)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		stream := mapValue(inbound["streamSettings"])
		if stringValue(stream["network"]) != "xhttp" || stringValue(stream["security"]) != "tls" || stringValue(inbound["listen"]) != "127.0.0.1" {
			t.Fatalf("%s is not loopback TLS XHTTP: %#v", tag, inbound)
		}
		host := TSinCloudHost(name, "x.example.com")
		if got := stringValue(mapValue(stream["tlsSettings"])["serverName"]); got != host {
			t.Fatalf("%s certificate name = %q, want %q", tag, got, host)
		}
		ports[host], _ = parseConfigPort(inbound["port"])
		var remark, address, sni, hostHeader string
		var port int
		if err := db.QueryRow(`SELECT remark, address, port, COALESCE(sni, ''), COALESCE(host, '') FROM hosts WHERE inbound_tag = ?`, tag).
			Scan(&remark, &address, &port, &sni, &hostHeader); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(remark, "☁️") || address != "render.com" || port != 443 || sni != host || hostHeader != host {
			t.Fatalf("%s host = %q %q %d %q %q", tag, remark, address, port, sni, hostHeader)
		}
	}
	if TSinCloudHost("Best ping", "x.example.com") != "best-ping.x.example.com" {
		t.Fatalf("name = %q", TSinCloudHost("Best ping", "x.example.com"))
	}
	if _, err := repo.GetInbound(ctx, TSinCloudInboundTag("Turkey")); err == nil {
		t.Fatal("Turkey got a Cloudflare XHTTP config it was not given")
	}

	config, err := repo.GetTargetRawConfig(ctx, "master")
	if err != nil {
		t.Fatal(err)
	}
	front := findInbound(config, TSinCloudFrontTag)
	if front == nil || stringValue(front["protocol"]) != "dokodemo-door" {
		t.Fatalf("front = %#v", front)
	}
	if port, _ := parseConfigPort(front["port"]); port != DefaultTSinCloudPort {
		t.Fatalf("front port = %d", port)
	}
	var hostRows int
	_ = db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, TSinCloudFrontTag).Scan(&hostRows)
	if hostRows != 0 {
		t.Fatal("the front got a host of its own")
	}
	redirects := map[string]string{}
	for _, item := range interfaceSlice(config["outbounds"]) {
		outbound := mapValue(item)
		redirects[stringValue(outbound["tag"])] = stringValue(mapValue(outbound["settings"])["redirect"])
	}
	rules := interfaceSlice(mapValue(config["routing"])["rules"])
	// The front's rules come first: one per name, then the drop for the rest.
	for i, name := range []string{"Germany", "Best ping"} {
		rule := mapValue(rules[i])
		host := TSinCloudHost(name, "x.example.com")
		if stringValue(interfaceSlice(rule["inboundTag"])[0]) != TSinCloudFrontTag || stringValue(interfaceSlice(rule["domain"])[0]) != "full:"+host {
			t.Fatalf("rule %d = %#v", i, rule)
		}
		want := "127.0.0.1:" + itoa(ports[host])
		if got := redirects[stringValue(rule["outboundTag"])]; got != want {
			t.Fatalf("%s hands over to %q, want %q", name, got, want)
		}
	}
	if drop := mapValue(rules[2]); stringValue(drop["outboundTag"]) != tsinCloudDropTag || drop["domain"] != nil {
		t.Fatalf("rule 2 = %#v, want the drop", drop)
	}
	// The country's rule carries its Cloudflare XHTTP inbound to its exit.
	found := false
	for _, item := range rules {
		rule := mapValue(item)
		if stringValue(rule["outboundTag"]) != TSinOutboundTag(1082) {
			continue
		}
		for _, tag := range interfaceSlice(rule["inboundTag"]) {
			if stringValue(tag) == TSinCloudInboundTag("Germany") {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("Germany's Cloudflare XHTTP inbound does not leave through Germany")
	}

	// Applying again keeps the inbounds (same port and path) and one front.
	again, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	for _, entry := range again {
		if entry.Cloud && entry.Created {
			t.Fatalf("%s was rebuilt on a plain re-apply", entry.Tag)
		}
	}
	config, _ = repo.GetTargetRawConfig(ctx, "master")
	fronts := 0
	for _, item := range interfaceSlice(config["inbounds"]) {
		if stringValue(mapValue(item)["tag"]) == TSinCloudFrontTag {
			fronts++
		}
	}
	if fronts != 1 {
		t.Fatalf("fronts = %d", fronts)
	}

	// No Cloudflare XHTTP countries: the inbounds, front and its routing go.
	for i := range countries {
		countries[i].Cloud = false
	}
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil); err != nil {
		t.Fatalf("apply without: %v", err)
	}
	config, _ = repo.GetTargetRawConfig(ctx, "master")
	if findInbound(config, TSinCloudFrontTag) != nil || findInbound(config, TSinCloudInboundTag("Germany")) != nil {
		t.Fatal("the Cloudflare XHTTP pieces are still there")
	}
	for _, item := range interfaceSlice(config["outbounds"]) {
		if strings.HasPrefix(stringValue(mapValue(item)["tag"]), tsinCloudOutboundPrefix) {
			t.Fatalf("outbound left: %v", mapValue(item)["tag"])
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func itoaUnused(n int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + func() string {
		digits := ""
		if n == 0 {
			return "0"
		}
		for n > 0 {
			digits = string(rune('0'+n%10)) + digits
			n /= 10
		}
		return digits
	}())
}
