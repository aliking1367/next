package api

import (
	"net/http/httptest"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

func supportPrincipal(role adminapp.AdminRole, changeConfig bool) adminPrincipal {
	perms := adminapp.RoleDefaultPermissions(role)
	perms.Sudo.ChangeConfig = changeConfig
	return adminPrincipal{
		Role:    string(role),
		Context: adminapp.EffectiveAdminContext{Admin: adminapp.Admin{Role: role, Permissions: perms}},
	}
}

func TestConfigChangeBlockedForSupportAccount(t *testing.T) {
	support := supportPrincipal(adminapp.RoleSudo, false)
	cases := []struct {
		method, path string
		blocked      bool
	}{
		{"GET", "/api/core/config", false},
		{"PUT", "/api/core/config", true},
		{"POST", "/api/core/restart", false},
		{"POST", "/api/core/verify-protocols", false},
		{"POST", "/api/core/tsin/apply", true},
		{"POST", "/api/core/cdn-hosts", true},
		{"POST", "/api/core/auto-configure", true},
		{"POST", "/api/node/3/restart", false},
		{"POST", "/api/node/3/reconnect", false},
		{"POST", "/api/node/3/sync", false},
		{"POST", "/api/node/3/service/restart", false},
		{"DELETE", "/api/node/3", true},
		{"PUT", "/api/node/3", true},
		{"POST", "/api/node", true},
		{"POST", "/api/node/3/host/reboot", true},
		{"POST", "/api/nodes/service/update", true},
		{"PUT", "/api/inbounds/vless-in", true},
		{"PUT", "/api/hosts", true},
		{"POST", "/api/v2/services", true},
		{"POST", "/api/panel/xray/testOutbounds", false},
		{"POST", "/api/user", false},
		{"PUT", "/api/user/alice", false},
		{"POST", "/api/admin", false},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		if got := configChangeBlocked(support, r); got != tc.blocked {
			t.Errorf("%s %s blocked=%v, want %v", tc.method, tc.path, got, tc.blocked)
		}
	}
}

func TestConfigChangeAllowedByDefault(t *testing.T) {
	for _, role := range []adminapp.AdminRole{adminapp.RoleSudo, adminapp.RoleFullAccess, adminapp.RoleStandard, adminapp.RoleReseller} {
		perms := adminapp.RoleDefaultPermissions(role)
		principal := adminPrincipal{Role: string(role), Context: adminapp.EffectiveAdminContext{Admin: adminapp.Admin{Role: role, Permissions: perms}}}
		r := httptest.NewRequest("PUT", "/api/core/config", nil)
		if configChangeBlocked(principal, r) {
			t.Fatalf("%s is blocked by default", role)
		}
	}
	// A sudo admin saved before this permission existed keeps changing config.
	perms, err := adminapp.BuildPermissions(adminapp.RoleSudo, `{"sudo":{"nodes":true,"xray":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !perms.Sudo.ChangeConfig {
		t.Fatal("stored sudo permissions without change_config lost it")
	}
}
