package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A country's tunnel config reaches the countries' server through the
// admin's Iranian tunnel instead of Cloudflare: the tunnel takes the user's
// connection inside Iran and forwards it, port for port, to this server.
// Tunnel services only carry VMess or Shadowsocks, and charge per port, so
// several countries share one port: each country has its own VMess over
// WebSocket inbound on loopback, and on each tunnel port a gate (VLESS over
// plain TCP with no users, like the Cloudflare gate) hands every connection
// to the country whose WebSocket path it asks for. The tunnel forwards each
// user connection as it is, so routing a connection by its first request is
// right here, unlike behind a CDN that mixes users on one connection.
//
// The gates' tags start with TSinTunnelGatePrefix; applying the countries
// rebuilds them every time, since their fallbacks follow the countries.

// TSinTunnelSuffix ends the tag of a country's tunnel inbound.
const TSinTunnelSuffix = "-tunnel"

// TSinTunnelGatePrefix starts the tag of a tunnel port's gate.
const TSinTunnelGatePrefix = TSinInboundPrefix + "tgate-"

// TSinTunnelMaxPerPort is how many countries one tunnel port carries.
const TSinTunnelMaxPerPort = 5

// tsinTunnelRemark marks a tunnel config in the subscription.
const tsinTunnelRemark = " 🛡"

// TSinTunnelInboundTag is the inbound that serves a country through the
// Iranian tunnel.
func TSinTunnelInboundTag(country string) string {
	return TSinInboundTag(country) + TSinTunnelSuffix
}

func tsinTunnelGateTag(port int) string { return TSinTunnelGatePrefix + strconv.Itoa(port) }

func buildTSinTunnelInbound(tag string, port int) (map[string]any, error) {
	path, err := randomXHTTPPath()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      tag,
		"listen":   "127.0.0.1",
		"port":     port,
		"protocol": "vmess",
		"settings": map[string]any{"clients": []any{}},
		"streamSettings": map[string]any{
			"network":    "ws",
			"security":   "none",
			"wsSettings": map[string]any{"path": path},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

func tsinTunnelInboundPath(inbound map[string]any) string {
	if stringValue(inbound["protocol"]) != "vmess" {
		return ""
	}
	return tsinInboundPath(inbound)
}

// ensureTSinTunnelInbound keeps or makes a country's tunnel inbound and points
// its config at the tunnel host on the country's tunnel port. An existing
// inbound keeps its port and path.
func (r Repository) ensureTSinTunnelInbound(ctx context.Context, target string, country TSinCountry, host string, used map[int]bool, ranges []portRange, portBusy func(int) bool) (TSinInbound, error) {
	tag := TSinTunnelInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportPath, Tunnel: true, TunnelPort: country.TunnelPort}
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing != nil && tsinTunnelInboundPath(existing) == "" {
		if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return entry, fmt.Errorf("rebuild %s: %w", tag, err)
		}
		existing = nil
	}
	if existing != nil {
		entry.Port, _ = parseConfigPort(existing["port"])
	} else {
		port, err := pickProvisionPort(nil, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		used[port] = true
		payload, err := buildTSinTunnelInbound(tag, port)
		if err != nil {
			return entry, fmt.Errorf("%s (tunnel): %w", country.Name, err)
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (tunnel): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	// No TLS: the tunnel only carries the connection, and VMess encrypts what
	// is inside. The path comes from the inbound. The port's own address and
	// tunnel-side port, when given, win over the shared ones.
	if address := strings.TrimSpace(country.TunnelAddress); address != "" {
		host = address
	}
	remotePort := country.TunnelRemotePort
	if remotePort <= 0 {
		remotePort = country.TunnelPort
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = ?, path = NULL, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'none' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinTunnelRemark, strings.TrimSpace(host), remotePort, tag)
	return entry, err
}

// applyTSinTunnelGates rebuilds one gate per tunnel port for the tunnel
// inbounds just applied, and removes the gates of ports no longer used.
func (r Repository) applyTSinTunnelGates(ctx context.Context, target string, inbounds []TSinInbound) error {
	all, err := r.CollectInboundTags(ctx)
	if err != nil {
		return err
	}
	for tag := range all {
		if strings.HasPrefix(tag, TSinTunnelGatePrefix) {
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return fmt.Errorf("retire %s: %w", tag, err)
			}
		}
	}
	byPort := map[int][]any{}
	for _, entry := range inbounds {
		if !entry.Tunnel || entry.TunnelPort <= 0 {
			continue
		}
		inbound, err := r.GetInbound(ctx, entry.Tag)
		if err != nil {
			return err
		}
		path := tsinTunnelInboundPath(inbound)
		if path == "" {
			return fmt.Errorf("%s has no path", entry.Tag)
		}
		byPort[entry.TunnelPort] = append(byPort[entry.TunnelPort], map[string]any{"path": path, "dest": entry.Port, "xver": 0})
	}
	if len(byPort) == 0 {
		return nil
	}
	current, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return err
	}
	ports := make([]int, 0, len(byPort))
	for port := range byPort {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	for _, port := range ports {
		for _, inbound := range listOfMaps(current["inbounds"]) {
			if existing, _ := parseConfigPort(inbound["port"]); existing == port {
				return fmt.Errorf("tunnel port %d is already used by %q on this server; choose another port", port, stringValue(inbound["tag"]))
			}
		}
		tag := tsinTunnelGateTag(port)
		payload := map[string]any{
			"tag":      tag,
			"listen":   "::",
			"port":     port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    []any{},
				"decryption": "none",
				"fallbacks":  byPort[port],
			},
			"streamSettings": map[string]any{"network": "tcp", "security": "none"},
			"targets":        []any{target},
		}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
		// A gate only forwards: no config of its own.
		if _, err := r.db.ExecContext(ctx, `DELETE FROM hosts WHERE inbound_tag = ?`, tag); err != nil {
			return err
		}
	}
	return nil
}
