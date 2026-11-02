package xrayconfig

import "testing"

func TestNormalizeVirtualTunnelSettingsUnescapesOneLinePEM(t *testing.T) {
	oneLine := `-----BEGIN CERTIFICATE-----\nMIIB\nAAAA\n-----END CERTIFICATE-----\n`
	want := "-----BEGIN CERTIFICATE-----\nMIIB\nAAAA\n-----END CERTIFICATE-----"
	for _, protocol := range []string{AnyConnectProtocol, OVProtocol} {
		settings := normalizeVirtualTunnelSettings(protocol, map[string]any{
			"ca":                 oneLine,
			"ca_certificate":     oneLine,
			"server_certificate": oneLine,
			"server_key":         `-----BEGIN PRIVATE KEY-----\r\nKEY\r\n-----END PRIVATE KEY-----`,
		})
		if got := settings["server_certificate"]; got != want {
			t.Fatalf("%s server_certificate = %q, want %q", protocol, got, want)
		}
		if got := settings["server_key"]; got != "-----BEGIN PRIVATE KEY-----\nKEY\n-----END PRIVATE KEY-----" {
			t.Fatalf("%s server_key = %q", protocol, got)
		}
	}
}

func TestNormalizeVirtualTunnelSettingsKeepsMultiLinePEM(t *testing.T) {
	pem := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"
	settings := normalizeVirtualTunnelSettings(AnyConnectProtocol, map[string]any{"server_certificate": pem})
	if got := settings["server_certificate"]; got != pem {
		t.Fatalf("server_certificate = %q, want %q", got, pem)
	}
}
