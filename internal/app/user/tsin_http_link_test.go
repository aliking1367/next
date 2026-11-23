package user

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// The T.Sin HTTP configs are VMess over TCP with an HTTP header: the link
// must carry the header type, the Host and the path the inbound names.
func TestVMessLinkCarriesTheHTTPHeaderOfATSinHTTPInbound(t *testing.T) {
	resolved, err := resolveInbound(map[string]any{
		"tag": "tsin-germany-http", "protocol": "vmess", "port": 31000,
		"settings": map[string]any{"clients": []any{}},
		"streamSettings": map[string]any{
			"network": "tcp", "security": "none",
			"tcpSettings": map[string]any{"header": map[string]any{
				"type":    "http",
				"request": map[string]any{"path": []any{"/"}, "headers": map[string]any{"Host": []any{"tgju.org"}}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	remark, address, effective, ok := effectiveInboundForHost("", nil, resolved, Host{ID: 1, Address: "167.233.180.133", Remark: "🇩🇪 Germany | همه اپراتورها"})
	if !ok {
		t.Fatal("host not usable")
	}
	link := vmessShareLink(remark, address, stringValue(effective["path"]), effective, map[string]any{"id": "00000000-0000-4000-8000-000000000001"})
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"add": "167.233.180.133", "net": "tcp", "type": "http", "host": "tgju.org", "path": "/"} {
		if stringValue(got[key]) != want {
			t.Fatalf("%s = %v, want %q (link %s)", key, got[key], want, raw)
		}
	}
	if tls := stringValue(got["tls"]); tls != "" && tls != "none" {
		t.Fatalf("tls = %q, want none", tls)
	}
}
