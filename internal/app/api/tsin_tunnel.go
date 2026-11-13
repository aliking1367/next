package api

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The tunnel countries (xrayconfig/tsin_tunnel.go) give chosen countries a
// config through the admin's Iranian tunnel, the one the emergency tunnel
// uses, several countries to a tunnel port. Which country is on which port is
// kept in tsin_settings.tunnel_countries, apart from the other settings so a
// read failure leaves every other T.Sin setting as it was.

// loadTSinTunnelCountries reads the countries and their tunnel ports, by
// country name in lower case. Any failure reads as none.
func (s *Server) loadTSinTunnelCountries(ctx context.Context) map[string]int {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(tunnel_countries, '') FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw); err != nil {
		return map[string]int{}
	}
	out := map[string]int{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) saveTSinTunnelCountries(ctx context.Context, countries map[string]int) error {
	encoded, err := json.Marshal(countries)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET tunnel_countries = ?`, string(encoded))
	return err
}

// cleanTSinTunnelCountries checks a choice of countries and ports: real ports,
// none the Cloudflare gate's, and at most TSinTunnelMaxPerPort on a port.
func cleanTSinTunnelCountries(values map[string]int, gatePort int) (map[string]int, error) {
	out := map[string]int{}
	perPort := map[int][]string{}
	for name, port := range values {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || len(name) > 60 || port == 0 {
			continue
		}
		if port < 1 || port > 65535 {
			return nil, fmt.Errorf("tunnel port %d is not a port", port)
		}
		if port == gatePort {
			return nil, fmt.Errorf("tunnel port %d is the Cloudflare gate's port; choose another", port)
		}
		out[name] = port
		perPort[port] = append(perPort[port], name)
	}
	for port, names := range perPort {
		if len(names) > xrayconfig.TSinTunnelMaxPerPort {
			sort.Strings(names)
			return nil, fmt.Errorf("tunnel port %d has %d countries (%s); one port carries at most %d", port, len(names), strings.Join(names, ", "), xrayconfig.TSinTunnelMaxPerPort)
		}
	}
	return out, nil
}

// loadTSinConnectAddress reads where the Cloudflare configs connect to while
// keeping the domain in use as SNI and Host; "" (also on any failure) is the
// domain itself.
func (s *Server) loadTSinConnectAddress(ctx context.Context) string {
	var address string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(cdn_connect_address, '') FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&address); err != nil {
		return ""
	}
	return strings.TrimSpace(address)
}

func (s *Server) saveTSinConnectAddress(ctx context.Context, address string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tsin_settings SET cdn_connect_address = ?`, strings.TrimSpace(address))
	return err
}

// tsinTunnelPort is one tunnel port: NodePort on the countries' server, which
// the countries' configs are grouped by, and where users reach it, Address
// (what the tunnel seller gave; empty is the emergency tunnel's address) and
// Port on the tunnel's side (0 is the same number as NodePort).
type tsinTunnelPort struct {
	NodePort int    `json:"node_port"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
}

// loadTSinTunnelPorts reads the tunnel ports; any failure reads as none.
func (s *Server) loadTSinTunnelPorts(ctx context.Context) []tsinTunnelPort {
	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(tunnel_ports, '') FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw); err != nil {
		return []tsinTunnelPort{}
	}
	out := []tsinTunnelPort{}
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (s *Server) saveTSinTunnelPorts(ctx context.Context, ports []tsinTunnelPort) error {
	encoded, err := json.Marshal(ports)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET tunnel_ports = ?`, string(encoded))
	return err
}

// cleanTSinTunnelPorts checks the tunnel ports: real ports, an address that is
// a host name or IP, and each port on the server listed once.
func cleanTSinTunnelPorts(values []tsinTunnelPort, gatePort int) ([]tsinTunnelPort, error) {
	out := make([]tsinTunnelPort, 0, len(values))
	seen := map[int]bool{}
	for _, item := range values {
		if item.NodePort == 0 && strings.TrimSpace(item.Address) == "" && item.Port == 0 {
			continue
		}
		if item.NodePort < 1 || item.NodePort > 65535 {
			return nil, fmt.Errorf("enter the port on the server for every tunnel row (%d is not a port)", item.NodePort)
		}
		if item.NodePort == gatePort {
			return nil, fmt.Errorf("port %d is the Cloudflare gate's port; choose another", item.NodePort)
		}
		if seen[item.NodePort] {
			return nil, fmt.Errorf("port %d on the server is listed twice", item.NodePort)
		}
		seen[item.NodePort] = true
		if item.Port < 0 || item.Port > 65535 {
			return nil, fmt.Errorf("tunnel port %d is not a port", item.Port)
		}
		address, err := normalizeTSinDirectAddress(item.Address)
		if err != nil {
			return nil, fmt.Errorf("the tunnel address %q must be a host name or an IP address, without a port", item.Address)
		}
		out = append(out, tsinTunnelPort{NodePort: item.NodePort, Address: address, Port: item.Port})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodePort < out[j].NodePort })
	return out, nil
}
