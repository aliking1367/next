package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The node server calls back on whatever address the admin reaches the panel
// at. Behind Cloudflare with a port rewrite, a stored setting would be wrong,
// so the address is taken from the request.
func TestPanelBaseURLFollowsTheRequest(t *testing.T) {
	request := httptest.NewRequest("GET", "http://panel.example.com/api/node/1/install-token", nil)
	request.Host = "panel.example.com"
	if got := panelBaseURL(request); got != "http://panel.example.com" {
		t.Errorf("plain request = %q", got)
	}

	request.Header.Set("X-Forwarded-Proto", "https")
	if got := panelBaseURL(request); got != "https://panel.example.com" {
		t.Errorf("forwarded scheme = %q", got)
	}

	request.Header.Set("X-Forwarded-Host", "public.example.com, internal")
	if got := panelBaseURL(request); got != "https://public.example.com" {
		t.Errorf("forwarded host = %q", got)
	}
}

func TestNodeInstallCommandAndStartupScript(t *testing.T) {
	command := nodeInstallCommand("https://panel.example.com", "tok123")
	// The installer path must stay under /api/, so one CDN rule covers every
	// call a node server makes. Outside it, a challenge answers the installer
	// with a 403.
	if !strings.Contains(command, "https://panel.example.com/api/node/install-script") {
		t.Errorf("command does not fetch the installer: %q", command)
	}
	if !strings.Contains(command, "--token tok123") {
		t.Errorf("command carries no token: %q", command)
	}
	if strings.Contains(command, "\n") {
		t.Errorf("the one-line command must stay on one line: %q", command)
	}

	script := nodeStartupScript("https://panel.example.com", "tok123")
	if !strings.HasPrefix(script, "#!") {
		t.Errorf("a startup script needs a shebang: %q", script)
	}
	if !strings.Contains(script, "--token tok123") {
		t.Errorf("startup script carries no token")
	}
}

// The installer runs unattended on first boot, so it has to refuse rather
// than half-install, and must not leave a node's private key lying around.
func TestNodeInstallScriptIsSafeUnattended(t *testing.T) {
	script := nodeInstallScript("https://panel.example.com")
	for _, want := range []string{
		"set -euo pipefail",
		"A --token is required",
		"Run this as root",
		"install-bundle?token=",
		"-----END CERTIFICATE-----",
		"--bundle-file",
		"</dev/null",
		"trap cleanup EXIT",
		"chmod 700",
		"ufw allow 62050/tcp",
		"ufw allow 62052/tcp",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("install script is missing %q", want)
		}
	}
	if strings.Contains(script, "--token") && strings.Contains(script, "echo $TOKEN") {
		t.Error("the script must not echo the token")
	}
}
