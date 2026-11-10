package user

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func smartTestBody(t *testing.T) string {
	t.Helper()
	const user = "11111111-1111-1111-1111-111111111111"
	cdn := func(path, name string) string {
		return "vless://" + user + "@g1.example.com:443?type=ws&security=tls&sni=g1.example.com&host=g1.example.com&path=%2F" + path + "#" + name
	}
	direct := func(port, name string) string {
		return "vless://" + user + "@203.0.113.7:" + port + "?type=xhttp&security=reality&sni=www.lenovo.com&pbk=YSWAz6a7KuQ6QbT5eQ98RgwjjBpk6Rhz7zK4MOVIoic&sid=12ecf2c2&fp=chrome&path=%2Fdx&mode=auto#" + name
	}
	body, err := renderXrayJSONSubscriptionWithMetadata([]string{
		cdn("de", "%F0%9F%87%A9%F0%9F%87%AA%20Germany"),
		cdn("bp", "Best%20ping"),
		cdn("pt", "%F0%9F%87%B5%F0%9F%87%B9%20Portugal"),
		direct("2053", "%F0%9F%87%A9%F0%9F%87%AA%20Germany%20%E2%9A%A1"),
		direct("2096", "Best%20ping%20%E2%9A%A1"),
	}, nil, false, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func smartDecode(t *testing.T, body string) []map[string]any {
	t.Helper()
	var configs []map[string]any
	if err := json.Unmarshal([]byte(body), &configs); err != nil {
		t.Fatal(err)
	}
	return configs
}

func smartAddress(outbound map[string]any) any {
	return listOfMaps(mapValue(outbound["settings"])["vnext"])[0]["address"]
}

func TestAddSmartConfigsOffLeavesBodyAlone(t *testing.T) {
	body := smartTestBody(t)
	if got := addSmartConfigs(body, smartMode("", SmartConfigOff)); got != body {
		t.Fatal("with the switch off and no ?smart= the subscription must stay byte for byte the same")
	}
	if got := addSmartConfigs("not json", smartModeOn); got != "not json" {
		t.Fatal("an unreadable body must be returned as it was")
	}
	noPair := `[{"remarks":"Germany","outbounds":[{"tag":"proxy","protocol":"vless"}]}]`
	if got := addSmartConfigs(noPair, smartModeOn); got != noPair {
		t.Fatal("a subscription without a direct and Cloudflare pair must stay the same")
	}
}

func TestSmartModeAndClientType(t *testing.T) {
	for _, tc := range []struct {
		query string
		level int
		want  string
	}{
		{"", SmartConfigOff, ""},
		{"", SmartConfigJSON, smartModeOn},
		{"", SmartConfigV2Box, smartModeOn},
		{"", 7, ""},
		{"1", SmartConfigOff, smartModeOn},
		{"fail", SmartConfigJSON, smartModeFail},
		{"nocdn", SmartConfigOff, smartModeNoCDN},
		{"junk", SmartConfigOff, ""},
	} {
		if got := smartMode(tc.query, tc.level); got != tc.want {
			t.Fatalf("smartMode(%q, %d) = %q, want %q", tc.query, tc.level, got, tc.want)
		}
	}
	for _, tc := range []struct {
		clientType, agent string
		level             int
		want              string
	}{
		{"v2ray", "V2Box/1.9 (iOS)", SmartConfigV2Box, "v2ray-json"},
		{"v2ray", "v2box", SmartConfigV2Box, "v2ray-json"},
		{"v2ray", "V2Box/1.9", SmartConfigJSON, "v2ray"},
		{"v2ray", "V2Box/1.9", SmartConfigOff, "v2ray"},
		{"v2ray", "v2rayNG/1.9", SmartConfigV2Box, "v2ray"},
		{"v2ray", "VaslPro/2", SmartConfigV2Box, "v2ray"},
		{"clash-meta", "V2Box/1.9", SmartConfigV2Box, "clash-meta"},
		{"happ", "Happ/1.70", SmartConfigV2Box, "happ"},
	} {
		if got := smartClientType(tc.clientType, tc.agent, tc.level); got != tc.want {
			t.Fatalf("smartClientType(%q, %q, %d) = %q, want %q", tc.clientType, tc.agent, tc.level, got, tc.want)
		}
	}
}

func TestAddSmartConfigsBuildsOneBestPingProfile(t *testing.T) {
	configs := smartDecode(t, addSmartConfigs(smartTestBody(t), smartModeOn))
	if len(configs) != 6 {
		t.Fatalf("want 1 smart + 5 originals, got %d", len(configs))
	}
	smart := configs[0]
	if smart["remarks"] != "⭐ هوشمند (بهترین لوکیشن)" {
		t.Fatalf("smart remark = %v", smart["remarks"])
	}
	outbounds := listOfMaps(smart["outbounds"])
	if outbounds[0]["tag"] != "proxy" || outbounds[1]["tag"] != "cdn" {
		t.Fatalf("outbound tags = %v, %v", outbounds[0]["tag"], outbounds[1]["tag"])
	}
	if address, port := smartAddress(outbounds[0]), listOfMaps(mapValue(outbounds[0]["settings"])["vnext"])[0]["port"]; address != "203.0.113.7" || port != float64(2096) {
		t.Fatalf("direct = %v:%v, want the Best ping one", address, port)
	}
	if path := mapValue(mapValue(outbounds[1]["streamSettings"])["wsSettings"])["path"]; path != "/bp" {
		t.Fatalf("fallback path = %v, want the Best ping one", path)
	}
	balancer := listOfMaps(mapValue(smart["routing"])["balancers"])[0]
	if balancer["fallbackTag"] != "cdn" || listAny(balancer["selector"])[0] != "proxy" {
		t.Fatalf("balancer = %v", balancer)
	}
	rules := listOfMaps(mapValue(smart["routing"])["rules"])
	if last := rules[len(rules)-1]; last["balancerTag"] != "smart" {
		t.Fatalf("last rule = %v", last)
	}
	// The originals follow untouched.
	for i, want := range []string{"🇩🇪 Germany", "Best ping", "🇵🇹 Portugal", "🇩🇪 Germany ⚡", "Best ping ⚡"} {
		if configs[i+1]["remarks"] != want {
			t.Fatalf("config %d remark = %v, want %s", i+1, configs[i+1]["remarks"], want)
		}
		if _, has := configs[i+1]["observatory"]; has {
			t.Fatalf("original config %d gained an observatory", i+1)
		}
	}
}

func TestAddSmartConfigsWithoutBestPingUsesFirstPair(t *testing.T) {
	body, err := renderXrayJSONSubscriptionWithMetadata([]string{
		"vless://11111111-1111-1111-1111-111111111111@g1.example.com:443?type=ws&security=tls&sni=g1.example.com&path=%2Fde#Germany",
		"vless://11111111-1111-1111-1111-111111111111@203.0.113.7:2053?type=tcp&security=reality&sni=www.lenovo.com&pbk=YSWAz6a7KuQ6QbT5eQ98RgwjjBpk6Rhz7zK4MOVIoic&sid=12ecf2c2&fp=chrome#Germany%20%E2%9A%A1",
	}, nil, false, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	configs := smartDecode(t, addSmartConfigs(body, smartModeOn))
	if len(configs) != 3 || configs[0]["remarks"] != "⭐ هوشمند (Germany)" {
		t.Fatalf("got %d configs, first %v", len(configs), configs[0]["remarks"])
	}
}

func TestAddSmartConfigsTestModesBreakOneWay(t *testing.T) {
	failed := smartDecode(t, addSmartConfigs(smartTestBody(t), smartModeFail))
	outbounds := listOfMaps(failed[0]["outbounds"])
	if smartAddress(outbounds[0]) != smartBrokenAddress || smartAddress(outbounds[1]) != "g1.example.com" {
		t.Fatalf("fail mode = %v / %v", smartAddress(outbounds[0]), smartAddress(outbounds[1]))
	}
	if !strings.Contains(stringValue(failed[0]["remarks"]), "تست") {
		t.Fatalf("test remark = %v", failed[0]["remarks"])
	}
	noCDN := smartDecode(t, addSmartConfigs(smartTestBody(t), smartModeNoCDN))
	outbounds = listOfMaps(noCDN[0]["outbounds"])
	if smartAddress(outbounds[0]) != "203.0.113.7" || smartAddress(outbounds[1]) != smartBrokenAddress {
		t.Fatalf("nocdn mode = %v / %v", smartAddress(outbounds[0]), smartAddress(outbounds[1]))
	}
	// The originals keep their real addresses.
	if smartAddress(listOfMaps(noCDN[5]["outbounds"])[0]) != "203.0.113.7" || smartAddress(listOfMaps(noCDN[2]["outbounds"])[0]) != "g1.example.com" {
		t.Fatal("a test mode changed an original config")
	}
}

// TestSmartConfigsPassRealXray runs `xray run -test` on each smart profile
// when NEXT_TEST_XRAY_BIN points at an xray executable.
func TestSmartConfigsPassRealXray(t *testing.T) {
	xrayBin := os.Getenv("NEXT_TEST_XRAY_BIN")
	if xrayBin == "" {
		t.Skip("set NEXT_TEST_XRAY_BIN to an xray executable to validate against real Xray")
	}
	source := smartTestBody(t)
	if path := os.Getenv("NEXT_TEST_SMART_SOURCE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source = string(raw)
	}
	var configs []map[string]any
	if err := json.Unmarshal([]byte(addSmartConfigs(source, smartModeOn)), &configs); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for i, config := range configs {
		if _, smart := config["observatory"]; !smart {
			continue
		}
		raw, _ := json.Marshal(config)
		path := filepath.Join(t.TempDir(), "smart.json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(xrayBin, "run", "-test", "-c", path).CombinedOutput(); err != nil {
			t.Fatalf("xray rejected smart config %d (%v): %v\n%s", i, config["remarks"], err, output)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no smart configs were produced")
	}
	t.Logf("%d smart configs passed xray -test", checked)
}
