package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

// An admin below full access editing their own account must not be able to
// change role, permissions, limits or services; password and contact fields
// stay theirs.
func TestAdminSelfEditDropsProtectedFields(t *testing.T) {
	raw := []byte(`{"role":"sudo","permissions":{"users":{"create":true}},"data_limit":0,"users_limit":0,"expire":0,
"services":[1,2],"service_limits":[],"require_2fa":false,"traffic_limit_mode":"created_traffic",
"password":"new-password","telegram_id":123,"subscription_domain":"sub.example.com"}`)
	var payload adminWritePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload.fields = map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &payload.fields); err != nil {
		t.Fatal(err)
	}
	payload.dropSelfProtectedFields()
	for _, field := range adminSelfProtectedFields {
		if _, ok := payload.fields[field]; ok {
			t.Fatalf("protected field %q survived", field)
		}
	}
	if payload.Role != "" || payload.Permissions != nil || payload.DataLimit != nil || payload.UsersLimit != nil ||
		payload.Expire != nil || payload.Services != nil || payload.ServiceLimits != nil || payload.Require2FA != nil || payload.TrafficLimitMode != nil {
		t.Fatalf("protected values survived: %+v", payload)
	}
	if payload.Password != "new-password" || payload.TelegramID == nil || payload.SubscriptionDomain == nil {
		t.Fatal("password, telegram id and subscription domain must stay editable")
	}
	for _, field := range []string{"password", "telegram_id", "subscription_domain"} {
		if _, ok := payload.fields[field]; !ok {
			t.Fatalf("editable field %q was dropped", field)
		}
	}
}

func TestAdminUsageResetAndSudoGrantRules(t *testing.T) {
	reseller := adminapp.Admin{Username: "seller", Role: adminapp.RoleReseller}
	owner := adminapp.Admin{Username: "owner", Role: adminapp.RoleFullAccess}
	manager := adminapp.Admin{Username: "manager", Role: adminapp.RoleSudo}
	manager.Permissions.AdminManagement.CanEdit = true

	if ensureCanResetAdminUsage(reseller, reseller) == nil {
		t.Fatal("a reseller reset their own usage")
	}
	if ensureCanResetAdminUsage(owner, owner) != nil {
		t.Fatal("full access may reset their own usage")
	}
	if ensureCanResetAdminUsage(manager, reseller) != nil {
		t.Fatal("an admin manager may reset a reseller's usage")
	}
	if canGrantSudo(manager) || canGrantSudo(reseller) || !canGrantSudo(owner) {
		t.Fatal("only full access or can_manage_sudo grants sudo")
	}
	manager.Permissions.AdminManagement.CanManageSudo = true
	if !canGrantSudo(manager) {
		t.Fatal("can_manage_sudo grants sudo")
	}
}

// Behind Cloudflare the TCP peer is an edge shared by everyone: the client
// comes from CF-Connecting-IP, but only when the peer really is Cloudflare.
func TestLoginClientIPTrustsHeadersOnlyFromProxies(t *testing.T) {
	for _, tc := range []struct {
		name, peer, header, value, want string
	}{
		{"cloudflare edge", "172.70.10.5:443", "CF-Connecting-IP", "203.0.113.7", "203.0.113.7"},
		{"cloudflare v6", "[2606:4700::1]:443", "CF-Connecting-IP", "203.0.113.8", "203.0.113.8"},
		{"forged from the internet", "198.51.100.9:5555", "CF-Connecting-IP", "203.0.113.7", "198.51.100.9"},
		{"forged real-ip from the internet", "198.51.100.9:5555", "X-Real-IP", "203.0.113.7", "198.51.100.9"},
		{"local proxy", "127.0.0.1:9000", "X-Real-IP", "203.0.113.10", "203.0.113.10"},
		{"garbage header", "172.70.10.5:443", "CF-Connecting-IP", "not-an-ip", "172.70.10.5"},
	} {
		r := httptest.NewRequest("POST", "/api/auth/login", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set(tc.header, tc.value)
		if got := loginClientIP(r); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// One address may not try a password against many accounts.
func TestLoginLimiterCapsOneAddressAcrossUsernames(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	limiter := &loginRateLimiter{now: func() time.Time { return now }}
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "198.51.100.20:4000"
	for i := 0; i < loginMaxFailuresPerIP; i++ {
		key := loginRateLimitKey(r, "user"+string(rune('a'+i)))
		if !limiter.allowed(key) {
			t.Fatalf("attempt %d refused too early", i)
		}
		limiter.recordFailure(key)
	}
	if limiter.allowed(loginRateLimitKey(r, "brand-new-user")) {
		t.Fatal("the address kept guessing after its per-address limit")
	}
	other := httptest.NewRequest("POST", "/api/auth/login", nil)
	other.RemoteAddr = "198.51.100.21:4000"
	if !limiter.allowed(loginRateLimitKey(other, "brand-new-user")) {
		t.Fatal("another address was blocked")
	}
}

func TestRedactInboundSecretsBlanksServerKeys(t *testing.T) {
	grouped := map[string][]map[string]any{
		"wireguard":   {{"tag": "wg", "settings": map[string]any{"private_key": "SECRET", "public_key": "pub", "peers": []any{map[string]any{"preSharedKey": "PSK"}}}}},
		"shadowsocks": {{"tag": "ss", "settings": map[string]any{"password": "SERVERPW", "method": "2022-blake3-aes-128-gcm"}}},
		"vless":       {{"tag": "v", "pbk": "public", "sni": []string{"a.example"}}},
	}
	redactInboundSecrets(grouped)
	wg := grouped["wireguard"][0]["settings"].(map[string]any)
	if wg["private_key"] != "" || wg["public_key"] != "pub" {
		t.Fatalf("wireguard: %#v", wg)
	}
	if peer := wg["peers"].([]any)[0].(map[string]any); peer["preSharedKey"] != "" {
		t.Fatalf("peer psk survived: %#v", peer)
	}
	ss := grouped["shadowsocks"][0]["settings"].(map[string]any)
	if ss["password"] != "" || ss["method"] == "" {
		t.Fatalf("shadowsocks: %#v", ss)
	}
	if grouped["vless"][0]["pbk"] != "public" {
		t.Fatal("public values must stay")
	}
}

func TestSecurityHeadersExceptTheMiniApp(t *testing.T) {
	handler := withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for path, framed := range map[string]bool{"/": false, "/api/users": false, "/sub/abc": false, "/miniapp": true, "/api/miniapp/me": true} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		got := w.Header().Get("X-Frame-Options")
		if framed && got != "" || !framed && got != "SAMEORIGIN" {
			t.Fatalf("%s: X-Frame-Options %q", path, got)
		}
	}
}
