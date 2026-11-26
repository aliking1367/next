package user

import (
	"net/url"
	"strings"
	"testing"
)

// The T.Sin TLS configs copy a seller's VLESS + TLS link: plain TCP, ordinary
// TLS, the server name as both address and SNI, and Chrome's fingerprint.
func TestVLESSLinkOfATSinTLSInbound(t *testing.T) {
	const name = "static.176.221.75.5.clients.your-server.de"
	resolved, err := resolveInbound(map[string]any{
		"tag": "tsin-germany-tls", "protocol": "vless", "port": 443,
		"settings": map[string]any{"clients": []any{}, "decryption": "none"},
		"streamSettings": map[string]any{
			"network": "tcp", "security": "tls",
			"tlsSettings": map[string]any{"serverName": name, "alpn": []any{"h2", "http/1.1"},
				"certificates": []any{map[string]any{"certificate": []any{"x"}, "key": []any{"y"}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sni := name
	remark, address, effective, ok := effectiveInboundForHost("", nil, resolved, Host{ID: 1, Address: name, SNI: &sni,
		Remark: "🇩🇪 Germany | TLS 🔐", Security: "inbound_default", ALPN: "none", Fingerprint: "chrome"})
	if !ok {
		t.Fatal("host not usable")
	}
	link := vlessShareLink(remark, address, stringValue(effective["path"]), effective, map[string]any{"id": "00000000-0000-4000-8000-000000000001"})
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatalf("%s: %v", link, err)
	}
	query := parsed.Query()
	if parsed.Hostname() != name || parsed.Port() != "443" {
		t.Fatalf("address = %s (link %s)", parsed.Host, link)
	}
	for key, want := range map[string]string{"security": "tls", "type": "tcp", "sni": name, "fp": "chrome"} {
		if query.Get(key) != want {
			t.Fatalf("%s = %q, want %q (link %s)", key, query.Get(key), want, link)
		}
	}
	if query.Get("flow") != "" || strings.Contains(link, "pbk=") {
		t.Fatalf("not a plain TLS link: %s", link)
	}
}

// A routed host puts its route number in the third group of the user's
// UUID, and nothing else in the link changes.
func TestVLESSRouteSettings(t *testing.T) {
	settings := map[string]any{"id": "3F3E3255-4D69-4686-8C82-902A498206AA", "flow": ""}
	routed := vlessRouteSettings("vless", settings, 1082)
	if routed == nil || routed["id"] != "3f3e3255-4d69-043a-8c82-902a498206aa" {
		t.Fatalf("routed = %#v", routed)
	}
	if settings["id"] != "3F3E3255-4D69-4686-8C82-902A498206AA" {
		t.Fatal("the shared settings were changed")
	}
	for _, bad := range []struct {
		protocol string
		id       string
	}{{"vmess", "3f3e3255-4d69-4686-8c82-902a498206aa"}, {"vless", "not-a-uuid"}, {"vless", ""}} {
		if vlessRouteSettings(bad.protocol, map[string]any{"id": bad.id}, 1082) != nil {
			t.Fatalf("%s %q got a routed link", bad.protocol, bad.id)
		}
	}
}
