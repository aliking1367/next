package xrayconfig

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
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
// sends the direct server. All countries share one inbound on port 443, owned
// by the chosen country: the others' links carry a route number in the
// user's UUID (Xray's VLESS route), and the routing sends each to its exit.

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

// TSinTLSRouteMax is the highest VLESS route number. Random (version 4)
// UUIDs carry 0x4000-0x4fff in those bytes, so numbers below that never
// match a user's own UUID, which reaches the inbound's owner.
const TSinTLSRouteMax = 0x3fff

// assignTSinTLSRoutes picks the country that owns the shared TLS inbound on
// port 443 (mainCountry when it has a TLS config, else the first that does)
// and gives every other TLS country a route number: its T.Sin port, which
// never changes, so the links stay the same from one apply to the next.
func assignTSinTLSRoutes(countries []TSinCountry, mainCountry string) ([]TSinCountry, error) {
	out := make([]TSinCountry, len(countries))
	copy(out, countries)
	owner := -1
	for i := range out {
		out[i].TLSRoute = 0
		if out[i].TLS && strings.EqualFold(strings.TrimSpace(mainCountry), out[i].Name) {
			owner = i
		}
	}
	for i := range out {
		if owner < 0 && out[i].TLS {
			owner = i
		}
	}
	seen := map[int]string{}
	for i := range out {
		if !out[i].TLS || i == owner {
			continue
		}
		route := out[i].Port
		if route <= 0 || route > TSinTLSRouteMax {
			return nil, fmt.Errorf("%s (TLS): its port %d cannot be used as a route number", out[i].Name, route)
		}
		if other, ok := seen[route]; ok {
			return nil, fmt.Errorf("%s and %s (TLS) share port %d", other, out[i].Name, route)
		}
		seen[route] = out[i].Name
		out[i].TLSRoute = route
	}
	return out, nil
}

// tsinTLSOwner is the country that owns the shared TLS inbound, if any.
func tsinTLSOwner(countries []TSinCountry) (TSinCountry, bool) {
	for _, country := range countries {
		if country.TLS && country.TLSRoute == 0 {
			return country, true
		}
	}
	return TSinCountry{}, false
}

// tsinTLSRouteRules sends each routed country's connections on the shared
// TLS inbound to its exit, by the route number in the user's UUID.
func tsinTLSRouteRules(countries []TSinCountry, split tsinRoutingSplit) []any {
	if split == tsinRouteMain {
		return nil
	}
	owner, ok := tsinTLSOwner(countries)
	if !ok {
		return nil
	}
	var rules []any
	for _, country := range countries {
		if !country.TLS || country.TLSRoute == 0 {
			continue
		}
		rules = append(rules, map[string]any{
			"type":        "field",
			"inboundTag":  []any{TSinTLSInboundTag(owner.Name)},
			"vlessRoute":  strconv.Itoa(country.TLSRoute),
			"outboundTag": TSinOutboundTag(country.Port),
		})
	}
	return rules
}

// ensureTSinTLSInbound keeps or makes the shared TLS inbound, owned by
// country, on port 443. A new server name (the old IP was blocked) swaps the
// certificate in place, so the users' links keep everything but the name.
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
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing, err = r.keepOrMoveTSinInbound(ctx, tag, existing, target); err != nil {
		return entry, err
	}
	if existing != nil {
		// One left on another port (made before the inbound was shared) moves
		// to 443.
		if port, _ := parseConfigPort(existing["port"]); port != 443 {
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return entry, fmt.Errorf("rebuild %s: %w", tag, err)
			}
			delete(used, port)
			existing = nil
		}
	}
	if existing != nil {
		entry.Port = 443
		if tsinTLSInboundServerName(existing) != serverName {
			payload := buildTSinTLSInbound(tag, entry.Port, serverName)
			payload["targets"] = []any{target}
			if _, err := r.UpdateInbound(ctx, tag, payload); err != nil {
				return entry, fmt.Errorf("%s (TLS): %w", country.Name, err)
			}
		}
	} else {
		port, err := pickProvisionPort([]int{443}, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		if port != 443 {
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
security = 'inbound_default', alpn = 'none', fingerprint = 'chrome' WHERE inbound_tag = ? AND COALESCE(vless_route, 0) = 0`,
		tsinHostRemark(country)+tsinTLSRemark, serverName, serverName, tag)
	return entry, err
}

// syncTSinTLSRouteHosts gives every routed TLS country a host on the shared
// inbound, carrying its route number, and drops those of countries no longer
// routed. The service picks the new hosts up with the inbound's own.
func (r Repository) syncTSinTLSRouteHosts(ctx context.Context, tag string, countries []TSinCountry, gate TSinGate) error {
	serverName, err := NormalizeTSinTLSServerName(gate.TLSServerName)
	if err != nil {
		return err
	}
	keep := map[int64]bool{}
	for _, country := range countries {
		if !country.TLS || country.TLSRoute == 0 {
			continue
		}
		remark := tsinHostRemark(country) + tsinTLSRemark
		var id int64
		err := r.db.QueryRowContext(ctx, `SELECT id FROM hosts WHERE inbound_tag = ? AND vless_route = ? ORDER BY id LIMIT 1`, tag, country.TLSRoute).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			result, err := r.db.ExecContext(ctx, `INSERT INTO hosts (remark, address, inbound_tag, sni, security, alpn, fingerprint, vless_route)
VALUES (?, ?, ?, ?, 'inbound_default', 'none', 'chrome', ?)`, remark, serverName, tag, serverName, country.TLSRoute)
			if err != nil {
				return fmt.Errorf("%s (TLS host): %w", country.Name, err)
			}
			if id, err = result.LastInsertId(); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if _, err := r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = NULL, path = NULL, sni = ?, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'chrome' WHERE id = ?`, remark, serverName, serverName, id); err != nil {
				return err
			}
		}
		keep[id] = true
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM hosts WHERE inbound_tag = ? AND COALESCE(vless_route, 0) <> 0`, tag)
	if err != nil {
		return err
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := r.db.ExecContext(ctx, `DELETE FROM service_hosts WHERE host_id = ?`, id); err != nil {
			return err
		}
		if _, err := r.db.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}
