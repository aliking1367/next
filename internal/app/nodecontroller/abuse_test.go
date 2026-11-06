package nodecontroller

import (
	"encoding/json"
	"testing"
)

func TestAbuseProtectionGoesFirstAndKeepsTheConfig(t *testing.T) {
	raw := jsonMap(`{
		"inbounds":[{"tag":"in","protocol":"vless"},{"tag":"keep","protocol":"vmess","sniffing":{"enabled":false}},{"tag":"wg","protocol":"wireguard"}],
		"outbounds":[{"protocol":"freedom","tag":"DIRECT"},{"protocol":"blackhole","tag":"BLOCK"}],
		"routing":{"rules":[{"type":"field","inboundTag":["tsin-de"],"outboundTag":"tsin-out-1080"}]}
	}`)
	raw = withAbuseProtection(raw)
	raw = withAbuseProtection(raw) // a second pass adds nothing twice

	outbounds := listOfMaps(raw["outbounds"])
	if len(outbounds) != 3 || stringValue(outbounds[0]["tag"]) != "DIRECT" || stringValue(outbounds[2]["tag"]) != abuseBlockTag {
		t.Fatalf("outbounds = %v: the default must stay first and the block be added once", outbounds)
	}
	rules := listOfMaps(mapValue(raw["routing"])["rules"])
	if len(rules) != 3 || stringValue(rules[0]["outboundTag"]) != abuseBlockTag || stringValue(rules[1]["port"]) != "25" {
		t.Fatalf("rules = %v: the two blocks must come before the T.Sin route", rules)
	}
	if stringValue(rules[2]["outboundTag"]) != "tsin-out-1080" {
		t.Fatalf("the existing rule was lost: %v", rules)
	}
	inbounds := listOfMaps(raw["inbounds"])
	sniff := mapValue(inbounds[0]["sniffing"])
	if sniff["enabled"] != true || sniff["routeOnly"] != true {
		t.Fatalf("sniffing on the vless inbound = %v", sniff)
	}
	if mapValue(inbounds[1]["sniffing"])["enabled"] != false {
		t.Fatal("an inbound that turned sniffing off was changed")
	}
	if _, set := inbounds[2]["sniffing"]; set {
		t.Fatal("a wireguard inbound got sniffing")
	}
}

func TestAbuseProtectionLeavesAConfigWithoutOutbounds(t *testing.T) {
	raw := jsonMap(`{"inbounds":[{"tag":"in","protocol":"vless"}]}`)
	before, _ := json.Marshal(raw)
	after, _ := json.Marshal(withAbuseProtection(raw))
	if string(before) != string(after) {
		t.Fatalf("a config without outbounds was changed: %s", after)
	}
}
