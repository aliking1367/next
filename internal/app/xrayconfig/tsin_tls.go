package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// A country's TLS config is VLESS over plain TCP with ordinary TLS and a real
// certificate, the way an Iranian seller runs it on Hetzner: the server name
// is the name the provider gives the IP (static.<reversed IP>.clients.
// your-server.de), so a censor sees a TLS connection to a Hetzner customer's
// own host with a valid Let's Encrypt certificate. The config's address is
// that same name, so it always reaches the IP the certificate belongs to.
//
// The certificate lives on the panel's server under TSinTLSCertDir/<name>/
// (fullchain.pem and privkey.pem); the panel inlines it into the config it
// sends the direct server. One chosen country takes port 443; the others get
// ports of their own.

// TSinTLSSuffix ends the tag of a country's TLS inbound.
const TSinTLSSuffix = "-tls"

// TSinTLSCertDir holds one folder per server name with its certificate.
// Tests point it at a temporary folder.
var TSinTLSCertDir = "/var/lib/next/certs/tsin-tls"

// tsinTLSRemark follows the country's name on a TLS config.
const tsinTLSRemark = " | TLS 🔐"

// TSinTLSInboundTag is the inbound that serves a country over VLESS + TLS.
func TSinTLSInboundTag(country string) string {
	return TSinInboundTag(country) + TSinTLSSuffix
}

// NormalizeTSinTLSServerName checks the name the TLS configs use: a plain
// host name, never an IP (a certificate is issued for a name).
func NormalizeTSinTLSServerName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	host, err := NormalizeCDNDomain(value)
	if err != nil || host == "" || !strings.Contains(host, ".") || strings.Trim(host, "0123456789.") == "" {
		return "", errors.New("the TLS configs' server name must be a host name such as static.1.2.3.4.clients.your-server.de, without https:// or a port")
	}
	return host, nil
}

// TSinTLSCertificatePaths are the certificate and key files for a server name.
func TSinTLSCertificatePaths(serverName string) (string, string) {
	dir := filepath.Join(TSinTLSCertDir, serverName)
	return filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
}

func buildTSinTLSInbound(tag string, port int, serverName string) map[string]any {
	certFile, keyFile := TSinTLSCertificatePaths(serverName)
	return map[string]any{
		"tag":      tag,
		"listen":   "::",
		"port":     port,
		"protocol": "vless",
		"settings": map[string]any{"clients": []any{}, "decryption": "none"},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "tls",
			"tlsSettings": map[string]any{
				"serverName": serverName,
				"alpn":       []any{"h2", "http/1.1"},
				"minVersion": "1.2",
				"certificates": []any{map[string]any{
					"certificateFile": certFile,
					"keyFile":         keyFile,
				}},
			},
		},
		"sniffing": autoProvisionSniffing(),
	}
}

// tsinTLSInboundServerName is the server name an existing TLS inbound serves,
// or "" when it is not one.
func tsinTLSInboundServerName(inbound map[string]any) string {
	if stringValue(inbound["protocol"]) != "vless" {
		return ""
	}
	stream := mapValue(inbound["streamSettings"])
	if !strings.EqualFold(stringValue(stream["network"]), "tcp") || !strings.EqualFold(stringValue(stream["security"]), "tls") {
		return ""
	}
	return strings.ToLower(stringValue(mapValue(stream["tlsSettings"])["serverName"]))
}

// ensureTSinTLSInbound keeps or makes a country's TLS inbound. A new server
// name (the old IP was blocked) swaps the certificate in place, so the port
// and the users' links stay; only the name in them changes.
func (r Repository) ensureTSinTLSInbound(ctx context.Context, target string, country TSinCountry, gate TSinGate, used map[int]bool, ranges []portRange, portBusy func(int) bool) (TSinInbound, error) {
	tag := TSinTLSInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportTCP, TLS: true}
	serverName, err := NormalizeTSinTLSServerName(gate.TLSServerName)
	if err != nil {
		return entry, err
	}
	if serverName == "" {
		return entry, errors.New("set the TLS configs' server name first")
	}
	main := strings.EqualFold(strings.TrimSpace(gate.TLSMainCountry), country.Name)
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing, err = r.keepOrMoveTSinInbound(ctx, tag, existing, target); err != nil {
		return entry, err
	}
	if existing != nil {
		port, _ := parseConfigPort(existing["port"])
		// The 443 country changed: the inbound moves to (or off) 443.
		if (main && port != 443) || (!main && port == 443) {
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return entry, fmt.Errorf("rebuild %s: %w", tag, err)
			}
			delete(used, port)
			existing = nil
		}
	}
	if existing != nil {
		entry.Port, _ = parseConfigPort(existing["port"])
		if tsinTLSInboundServerName(existing) != serverName {
			payload := buildTSinTLSInbound(tag, entry.Port, serverName)
			payload["targets"] = []any{target}
			if _, err := r.UpdateInbound(ctx, tag, payload); err != nil {
				return entry, fmt.Errorf("%s (TLS): %w", country.Name, err)
			}
		}
	} else {
		var preferred []int
		if main {
			preferred = []int{443}
		}
		port, err := pickProvisionPort(preferred, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		if main && port != 443 {
			return entry, fmt.Errorf("%s (TLS): port 443 is already taken on the direct server", country.Name)
		}
		used[port] = true
		payload := buildTSinTLSInbound(tag, port, serverName)
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (TLS): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = NULL, path = NULL, sni = ?, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'chrome' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinTLSRemark, serverName, serverName, tag)
	return entry, err
}
