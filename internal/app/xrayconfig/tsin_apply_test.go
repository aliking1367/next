//go:build cgo

package xrayconfig

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestApplyTSinCreatesOneInboundPerCountryAndRoutesIt(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1"},
		{Port: 1095, Name: "Turkey", Code: "TR", Address: "10.0.0.1"},
	}

	first, err := repo.ApplyTSin(ctx, "master", "tcp", TSinGate{}, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(first) != 2 || !first[0].Created || first[0].Port == first[1].Port {
		t.Fatalf("first apply = %#v", first)
	}
	// Applying again keeps the same inbounds and ports: users' links must not move.
	second, err := repo.ApplyTSin(ctx, "master", "tcp", TSinGate{}, countries, nil)
	if err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if second[0].Created || second[0].Port != first[0].Port || second[1].Port != first[1].Port {
		t.Fatalf("re-apply changed the inbounds: %#v vs %#v", first, second)
	}

	config, err := repo.MasterRawConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules := interfaceSlice(mapValue(config["routing"])["rules"])
	if len(rules) < 2 || stringValue(mapValue(rules[0])["outboundTag"]) != "tsin-out-1082" {
		t.Fatalf("rules = %#v", rules)
	}
	if len(listOfMaps(config["outbounds"])) < 2 {
		t.Fatalf("outbounds = %#v", config["outbounds"])
	}

	var remark string
	if err := db.QueryRow(`SELECT remark FROM hosts WHERE inbound_tag = 'tsin-germany'`).Scan(&remark); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(remark, "\U0001F1E9\U0001F1EA") || !strings.Contains(remark, "Germany") {
		t.Fatalf("remark = %q", remark)
	}

	// Dropping a country retires its inbound and its rule, and leaves the other.
	if _, err := repo.ApplyTSin(ctx, "master", "tcp", TSinGate{}, countries[:1], nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, "tsin-turkey"); err == nil {
		t.Fatal("tsin-turkey was not retired")
	}
	if err := repo.RemoveTSin(ctx); err != nil {
		t.Fatal(err)
	}
	config, _ = repo.MasterRawConfig(ctx)
	for _, outbound := range listOfMaps(config["outbounds"]) {
		if strings.HasPrefix(stringValue(outbound["tag"]), TSinOutboundPrefix) {
			t.Fatalf("outbound left behind: %#v", outbound)
		}
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany"); err == nil {
		t.Fatal("tsin-germany was not removed")
	}
}

func TestApplyTSinCanMoveToANodeAndLeavesNothingBehind(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{{Port: 1082, Name: "Germany", Code: "DE"}}

	if _, err := repo.ApplyTSin(ctx, "master", "tcp", TSinGate{}, countries, nil); err != nil {
		t.Fatalf("apply on master: %v", err)
	}
	moved, err := repo.ApplyTSin(ctx, "node:7", "tcp", TSinGate{}, countries, nil)
	if err != nil {
		t.Fatalf("apply on node: %v", err)
	}
	if len(moved) != 1 || !moved[0].Created {
		t.Fatalf("the inbound was not rebuilt on the node: %#v", moved)
	}

	direct, err := repo.directTargetsForInbound(ctx, "tsin-germany")
	if err != nil || len(direct) != 1 || direct[0] != "node:7" {
		t.Fatalf("targets = %v, err = %v", direct, err)
	}
	master, err := repo.MasterRawConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if hasTSinRouting(master) {
		t.Fatalf("master still routes T.Sin after the move: %#v", master["outbounds"])
	}
	node, err := repo.GetTargetRawConfig(ctx, "node:7")
	if err != nil {
		t.Fatal(err)
	}
	if !hasTSinRouting(node) {
		t.Fatalf("the node has no T.Sin routing: %#v", node["outbounds"])
	}

	if err := repo.RemoveTSin(ctx); err != nil {
		t.Fatal(err)
	}
	node, _ = repo.GetTargetRawConfig(ctx, "node:7")
	if hasTSinRouting(node) {
		t.Fatal("removal left routing on the node")
	}
	if _, err := repo.GetInbound(ctx, "tsin-germany"); err == nil {
		t.Fatal("the inbound survived removal")
	}
}

func TestApplyTSinSwitchesTransportByRebuilding(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{{Port: 1082, Name: "Germany", Code: "DE"}}

	if _, err := repo.ApplyTSin(ctx, "master", "tcp", TSinGate{}, countries, nil); err != nil {
		t.Fatal(err)
	}
	switched, err := repo.ApplyTSin(ctx, "master", "xhttp", TSinGate{}, countries, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !switched[0].Created || switched[0].Transport != "xhttp" {
		t.Fatalf("the inbound was not rebuilt on XHTTP: %#v", switched)
	}
	inbound, err := repo.GetInbound(ctx, "tsin-germany")
	if err != nil {
		t.Fatal(err)
	}
	stream := mapValue(inbound["streamSettings"])
	if stringValue(stream["network"]) != "xhttp" || stringValue(stream["security"]) != "reality" {
		t.Fatalf("stream = %#v", stream)
	}
	again, err := repo.ApplyTSin(ctx, "master", "xhttp", TSinGate{}, countries, nil)
	if err != nil || again[0].Created || again[0].Port != switched[0].Port {
		t.Fatalf("re-applying the same transport moved the inbound: %#v %v", again, err)
	}
}

func TestApplyTSinPathModeBuildsOneGateWithAFallbackPerCountry(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE"},
		{Port: 1095, Name: "Turkey", Code: "TR"},
	}
	gate := TSinGate{Host: "g1.example.com"}

	applied, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied = %#v", applied)
	}
	gateInbound, err := repo.GetInbound(ctx, TSinGateTag)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if port, _ := parseConfigPort(gateInbound["port"]); port != DefaultTSinGatePort {
		t.Fatalf("gate port = %v", gateInbound["port"])
	}
	fallbacks := listOfMaps(mapValue(gateInbound["settings"])["fallbacks"])
	if len(fallbacks) != 2 {
		t.Fatalf("fallbacks = %#v", gateInbound["settings"])
	}
	for i, country := range applied {
		inbound, err := repo.GetInbound(ctx, country.Tag)
		if err != nil {
			t.Fatal(err)
		}
		if stringValue(inbound["listen"]) != "127.0.0.1" || tsinInboundTransport(inbound) != "path" {
			t.Fatalf("%s is not a loopback WebSocket inbound: %#v", country.Tag, inbound)
		}
		if stringValue(fallbacks[i]["path"]) != tsinInboundPath(inbound) {
			t.Fatalf("fallback %d path %v != inbound path %q", i, fallbacks[i]["path"], tsinInboundPath(inbound))
		}
		if port, _ := parseConfigPort(fallbacks[i]["dest"]); port != country.Port {
			t.Fatalf("fallback %d goes to %v, inbound is on %d", i, fallbacks[i]["dest"], country.Port)
		}
	}

	var address, sni, security string
	var port sql.NullInt64
	if err := db.QueryRow(`SELECT address, port, sni, security FROM hosts WHERE inbound_tag = 'tsin-germany'`).Scan(&address, &port, &sni, &security); err != nil {
		t.Fatal(err)
	}
	if address != "g1.example.com" || port.Int64 != 443 || sni != "g1.example.com" || security != "tls" {
		t.Fatalf("country host = %s:%d sni=%s security=%s", address, port.Int64, sni, security)
	}
	var gateHosts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, TSinGateTag).Scan(&gateHosts); err != nil || gateHosts != 0 {
		t.Fatalf("the gate must not have a host of its own: %d %v", gateHosts, err)
	}

	// Leaving path mode removes the gate and points the hosts back at the node.
	if _, err := repo.ApplyTSin(ctx, "master", "xhttp", TSinGate{}, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinGateTag); err == nil {
		t.Fatal("the gate survived leaving path mode")
	}
	if err := db.QueryRow(`SELECT address, security FROM hosts WHERE inbound_tag = 'tsin-germany'`).Scan(&address, &security); err != nil {
		t.Fatal(err)
	}
	if address != "{SERVER_IP}" || security != "inbound_default" {
		t.Fatalf("host was not restored: %s %s", address, security)
	}

	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "not a domain"}, countries, nil); err == nil {
		t.Fatal("path mode accepted a bad domain")
	}
}

// In path mode a country can also get a direct config: REALITY on the
// server's own IP, routed to the same exit, outside the gate.
func TestApplyTSinAddsDirectConfigsOnlyForTheChosenCountries(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Direct: true},
		{Port: 1095, Name: "Turkey", Code: "TR"},
	}
	applied, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(applied) != 3 {
		t.Fatalf("applied = %#v, want two countries and one direct config", applied)
	}
	direct, err := repo.GetInbound(ctx, TSinDirectInboundTag("Germany"))
	if err != nil {
		t.Fatalf("direct inbound: %v", err)
	}
	stream := mapValue(direct["streamSettings"])
	if stringValue(stream["network"]) != "xhttp" || stringValue(stream["security"]) != "reality" {
		t.Fatalf("direct inbound is not REALITY over XHTTP: %#v", stream)
	}
	if _, err := repo.GetInbound(ctx, TSinDirectInboundTag("Turkey")); err == nil {
		t.Fatal("Turkey got a direct config it was not given")
	}
	gate, err := repo.GetInbound(ctx, TSinGateTag)
	if err != nil {
		t.Fatal(err)
	}
	if fallbacks := listOfMaps(mapValue(gate["settings"])["fallbacks"]); len(fallbacks) != 2 {
		t.Fatalf("the gate must only carry the path configs: %#v", fallbacks)
	}
	var remark, address string
	if err := db.QueryRow(`SELECT remark, address FROM hosts WHERE inbound_tag = ?`, TSinDirectInboundTag("Germany")).Scan(&remark, &address); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(remark, "⚡") || address != "{SERVER_IP}" {
		t.Fatalf("direct host = %q at %q", remark, address)
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
			if tag == TSinDirectInboundTag("Germany") {
				routed = true
			}
		}
	}
	if !routed {
		t.Fatal("the direct config is not routed to Germany's exit")
	}

	// Taking Germany off the direct list removes its direct config.
	countries[0].Direct = false
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinDirectInboundTag("Germany")); err == nil {
		t.Fatal("the direct config survived being turned off")
	}
}

// Where the machine's free ports are known, direct configs take the ports
// HTTPS sites use, skipping one something else listens on, and point at the
// extra IP kept for them.
func TestApplyTSinDirectConfigsTakeHTTPSPortsAndTheExtraIP(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Direct: true},
		{Port: 1095, Name: "Turkey", Code: "TR", Direct: true},
	}
	busy := func(port int) bool { return port == 443 } // say a web server holds it
	gate := TSinGate{Host: "g1.example.com", DirectAddress: "203.0.113.7"}
	if _, err := repo.ApplyTSin(ctx, "master", "path", gate, countries, busy); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ports := map[int]bool{}
	for _, name := range []string{"Germany", "Turkey"} {
		inbound, err := repo.GetInbound(ctx, TSinDirectInboundTag(name))
		if err != nil {
			t.Fatal(err)
		}
		port, _ := parseConfigPort(inbound["port"])
		ports[port] = true
		var address string
		if err := db.QueryRow(`SELECT address FROM hosts WHERE inbound_tag = ?`, TSinDirectInboundTag(name)).Scan(&address); err != nil {
			t.Fatal(err)
		}
		if address != "203.0.113.7" {
			t.Fatalf("%s points at %q, want the extra IP", name, address)
		}
	}
	if !ports[8443] || !ports[2053] {
		t.Fatalf("ports = %v, want 8443 and 2053 with 443 taken", ports)
	}

	// Without a way to check the machine, a random high port, as before.
	repo2, _ := provisionTestRepository(t)
	if _, err := repo2.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries[:1], nil); err != nil {
		t.Fatal(err)
	}
	inbound, _ := repo2.GetInbound(ctx, TSinDirectInboundTag("Germany"))
	if port, _ := parseConfigPort(inbound["port"]); slicesContains(tsinDirectPorts, port) {
		t.Fatalf("an unchecked machine got port %d", port)
	}
}

func slicesContains(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// The emergency tunnel's inbounds are made once, routed straight out, kept
// by later applies of the countries, and only shown or hidden by the switch.
func TestTSinEmergencyInboundsSurviveAppliesAndToggleByHost(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	countries := []TSinCountry{{Port: 1082, Name: "Germany", Code: "DE"}}
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}
	ports, err := repo.EnsureTSinEmergency(ctx, "master", nil)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if ports.VMess == 0 || ports.Shadowsocks == 0 || ports.VMess == ports.Shadowsocks {
		t.Fatalf("ports = %+v", ports)
	}
	again, err := repo.EnsureTSinEmergency(ctx, "master", nil)
	if err != nil || again != ports {
		t.Fatalf("a second ensure moved the ports: %+v -> %+v (%v)", ports, again, err)
	}
	config, _ := repo.GetTargetRawConfig(ctx, "master")
	routed := 0
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		if stringValue(rule["outboundTag"]) == tsinEmergencyOutbound {
			routed++
		}
	}
	outbounds := listOfMaps(config["outbounds"])
	if routed != 1 || stringValue(outbounds[len(outbounds)-1]["tag"]) != tsinEmergencyOutbound {
		t.Fatalf("routing = %v, outbounds = %v", config["routing"], outbounds)
	}

	if err := repo.SetTSinEmergencyHosts(ctx, "185.1.2.3", 4433, 0, true, "both", nil); err != nil {
		t.Fatal(err)
	}
	var address string
	var disabled int
	var port sql.NullInt64
	if err := db.QueryRow(`SELECT address, port, COALESCE(is_disabled, 0) FROM hosts WHERE inbound_tag = ?`, TSinEmergencyVMessTag).Scan(&address, &port, &disabled); err != nil {
		t.Fatal(err)
	}
	if address != "185.1.2.3" || port.Int64 != 4433 || disabled != 0 {
		t.Fatalf("vmess host = %s:%d disabled=%d", address, port.Int64, disabled)
	}
	if err := db.QueryRow(`SELECT port FROM hosts WHERE inbound_tag = ?`, TSinEmergencySSTag).Scan(&port); err != nil || port.Valid {
		t.Fatalf("a 0 port must keep the inbound's own: %v %v", port, err)
	}

	// Applying the countries again keeps them.
	if _, err := repo.ApplyTSin(ctx, "master", "path", TSinGate{Host: "g1.example.com"}, countries, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinEmergencyVMessTag); err != nil {
		t.Fatalf("an apply removed the emergency tunnel: %v", err)
	}

	// With one tunnel port only the chosen protocol is shown.
	if err := repo.SetTSinEmergencyHosts(ctx, "185.1.2.3", 4433, 0, true, "vmess", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COALESCE(is_disabled, 0) FROM hosts WHERE inbound_tag = ?`, TSinEmergencySSTag).Scan(&disabled); err != nil || disabled != 1 {
		t.Fatalf("Shadowsocks shown though only VMess was chosen: %d %v", disabled, err)
	}
	if err := repo.SetTSinEmergencyHosts(ctx, "185.1.2.3", 4433, 0, false, "both", []int64{7}); err != nil {
		t.Fatal(err)
	}
	var admins string
	if err := db.QueryRow(`SELECT COALESCE(is_disabled, 0), COALESCE(enabled_admins, '') FROM hosts WHERE inbound_tag = ?`, TSinEmergencyVMessTag).Scan(&disabled, &admins); err != nil || disabled != 1 || admins != "[7]" {
		t.Fatalf("with the owner's switch off and admin 7 on: disabled=%d admins=%q %v", disabled, admins, err)
	}

	if err := repo.RemoveTSinEmergency(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetInbound(ctx, TSinEmergencySSTag); err == nil {
		t.Fatal("the emergency inbound survived removal")
	}
}
