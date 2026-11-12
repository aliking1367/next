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
