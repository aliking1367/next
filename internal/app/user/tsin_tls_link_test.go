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
