package xrayconfig

import "testing"

func TestTSinRoutingReplacesOnlyItsOwnEntries(t *testing.T) {
	config := map[string]any{
		"outbounds": []any{
			map[string]any{"tag": "direct", "protocol": "freedom"},
			map[string]any{"tag": "tsin-out-1111", "protocol": "socks"},
		},
		"routing": map[string]any{"rules": []any{
			map[string]any{"type": "field", "inboundTag": []any{"old"}, "outboundTag": "tsin-out-1111"},
			map[string]any{"type": "field", "ip": []any{"geoip:private"}, "outboundTag": "blocked"},
		}},
	}
	setTSinRouting(config, []TSinCountry{
		{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1"},
		{Port: 1095, Name: "Turkey", Code: "TR"},
	})
	for i := 0; i < 2; i++ { // applying twice must not stack entries
		setTSinRouting(config, []TSinCountry{
			{Port: 1082, Name: "Germany", Code: "DE", Address: "10.0.0.1"},
			{Port: 1095, Name: "Turkey", Code: "TR"},
		})
	}

	outbounds := listOfMaps(config["outbounds"])
	if len(outbounds) != 3 || stringValue(outbounds[0]["tag"]) != "direct" {
		t.Fatalf("outbounds = %#v", outbounds)
	}
	server := listOfMaps(mapValue(outbounds[1]["settings"])["servers"])[0]
	if server["address"] != "10.0.0.1" || server["port"] != 1082 {
		t.Fatalf("server = %#v", server)
	}
	rules := interfaceSlice(mapValue(config["routing"])["rules"])
	if len(rules) != 3 {
		t.Fatalf("rules = %#v", rules)
	}
	first := mapValue(rules[0])
	if stringValue(first["outboundTag"]) != "tsin-out-1082" || stringValue(interfaceSlice(first["inboundTag"])[0]) != "tsin-germany" {
		t.Fatalf("first rule = %#v", first)
	}
	if stringValue(mapValue(rules[2])["outboundTag"]) != "blocked" {
		t.Fatalf("the admin's own rule was lost: %#v", rules)
	}

	setTSinRouting(config, nil)
	if len(listOfMaps(config["outbounds"])) != 1 || len(interfaceSlice(mapValue(config["routing"])["rules"])) != 1 {
		t.Fatalf("removal left T.Sin entries behind: %#v", config)
	}
}

func TestTSinTagsAndFlags(t *testing.T) {
	for name, want := range map[string]string{
		"Germany": "tsin-germany", "The Netherlands": "tsin-the-netherlands",
		"France dc": "tsin-france-dc", "United States": "tsin-united-states",
	} {
		if got := TSinInboundTag(name); got != want {
			t.Fatalf("tag(%q) = %q, want %q", name, got, want)
		}
	}
	if TSinFlag("de") != "\U0001F1E9\U0001F1EA" || TSinFlag("x") != "" || TSinFlag("1a") != "" {
		t.Fatalf("flags: %q %q %q", TSinFlag("de"), TSinFlag("x"), TSinFlag("1a"))
	}
}
