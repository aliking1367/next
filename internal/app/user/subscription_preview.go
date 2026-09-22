package user

import "time"

// RenderSubscriptionPagePreview renders a subscription page template for a
// fixed demo user, so admins can compare templates before choosing one. No
// real user data is involved.
func RenderSubscriptionPagePreview(content string) (string, error) {
	limit := int64(50 << 30)
	expire := time.Now().Add(24 * 24 * time.Hour).Unix()
	service := "Premium"
	user := UserDetail{
		Username:               "demo_user",
		Status:                 "active",
		UsedTraffic:            21474836480, // 20 GB
		DataLimit:              &limit,
		Expire:                 &expire,
		DataLimitResetStrategy: "month",
		ServiceName:            &service,
		SubscriptionURL:        "https://example.com/sub/demo",
	}
	links := []string{
		"vless://00000000-0000-4000-8000-000000000000@203.0.113.10:443?security=reality&type=tcp&flow=xtls-rprx-vision#Germany%20Vision",
		"vless://00000000-0000-4000-8000-000000000000@203.0.113.10:8443?security=reality&type=xhttp#Germany%20XHTTP",
		"hysteria2://demo@203.0.113.20:45927/?alpn=h3#Netherlands%20Hysteria2",
		"trojan://demo@cdn.example.com:2096?security=tls&type=ws#CDN%20Trojan",
	}
	vpn := map[string]any{
		"wireguard": map[string]any{"profiles": []WGProfile{{Remark: "WireGuard Amsterdam", Filename: "wg.conf", DownloadURL: "#"}}},
	}
	return renderSubscriptionPageTemplate(content, user, links, "#", "#", "preview", vpn)
}
