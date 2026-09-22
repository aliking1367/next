package user

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSubscriptionPageTemplatesRender renders every bundled subscription page
// with realistic data, so a template error never reaches users. Set
// NEXT_TEMPLATE_PREVIEW_DIR to also write the rendered pages for a visual check.
func TestSubscriptionPageTemplatesRender(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "templates", "subscription", "*.html"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no subscription templates found: %v", err)
	}
	limit := int64(50 << 30)
	expire := time.Now().Add(20 * 24 * time.Hour).Unix()
	service := "Premium"
	active := UserDetail{
		ID:                     1,
		Username:               "ali_test",
		Status:                 "active",
		UsedTraffic:            19756849561, // about 18.4 GB
		DataLimit:              &limit,
		Expire:                 &expire,
		DataLimitResetStrategy: "month",
		ServiceName:            &service,
		SubscriptionURL:        "https://panel.example.com/sub/YWxpX3Rlc3Q/abc",
	}
	links := []string{
		"vless://3704b0ea-0965-fa12-96a0-a02b2d648f0a@95.179.160.136:443?security=reality&type=tcp&flow=xtls-rprx-vision&sni=www.microsoft.com#%F0%9F%87%A9%F0%9F%87%AA%20Germany%20Vision",
		"vless://3704b0ea-0965-fa12-96a0-a02b2d648f0a@panel.example.com:2096?security=tls&type=xhttp&path=%2Fx&host=a&b=%22q%22#CDN%20%3Cfast%3E",
		"hysteria2://9894d7124b32bfc0469138e2b837fd26@95.179.160.136:45927/?alpn=h3&pinSHA256=AB#Hysteria2",
		"vmess://eyJ2IjoiMiIsInBzIjoiVk1lc3MgTkwiLCJhZGQiOiIxLjIuMy40IiwicG9ydCI6IjgwIn0",
		"https://panel.example.com/sub/abc/openvpn/1.ovpn",
	}
	vpn := map[string]any{
		"wireguard": map[string]any{"profiles": []WGProfile{{Remark: "WG Amsterdam", Filename: "wg1.conf", DownloadURL: "https://panel.example.com/wg1.conf", Link: "wireguard://x@1.2.3.4:51820#wg"}}},
		"openvpn":   map[string]any{"profiles": []OVProfile{{Remark: "OpenVPN TCP", Filename: "ov.ovpn", DownloadURL: "https://panel.example.com/ov.ovpn"}}},
		"l2tp":      []L2TPInfo{{HostName: "Iran Relay", Server: "vpn.example.com", Username: "ali", Password: "p<a>ss", IPSecPSK: "psk123"}},
		"ikev2":     []RemoteAccessInfo{{Server: "ike.example.com", Port: 500, Username: "ali", Password: "secret"}},
	}
	expired := active
	expired.Status = "expired"

	previewDir := strings.TrimSpace(os.Getenv("NEXT_TEMPLATE_PREVIEW_DIR"))
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		name := strings.TrimSuffix(filepath.Base(path), ".html")
		for _, variant := range []struct {
			suffix string
			user   UserDetail
		}{{"", active}, {"-expired", expired}} {
			page, err := renderSubscriptionPageTemplate(string(raw), variant.user, links, "/sub/abc/usage", "https://t.me/support", "abc", vpn)
			if err != nil {
				t.Fatalf("%s%s: render failed: %v", name, variant.suffix, err)
			}
			if !strings.Contains(page, "ali_test") {
				t.Fatalf("%s%s: username missing", name, variant.suffix)
			}
			// User-controlled values must arrive HTML-escaped.
			if strings.Contains(page, "CDN <fast>") || strings.Contains(page, `p<a>ss`) {
				t.Fatalf("%s%s: unescaped value in output", name, variant.suffix)
			}
			if previewDir != "" {
				if err := os.WriteFile(filepath.Join(previewDir, name+variant.suffix+".html"), []byte(page), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
