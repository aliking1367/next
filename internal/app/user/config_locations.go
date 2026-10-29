package user

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/aliking1367/next/internal/app/countryflag"
)

// ConfigLocation is one node that serves the shared Xray config. Hosts whose
// address is {SERVER_IP} are offered once per location ("multi-location"),
// so a subscription lists every country the panel runs nodes in.
type ConfigLocation struct {
	Name    string
	Address string
	// TunnelAddress is where users reach this node when it is fronted by a
	// tunnel. Empty means the node has none, and a host pointing at {TUNNEL}
	// then skips it rather than handing out a config that reaches nothing.
	TunnelAddress string

	// onlyTags limits a node on its own Xray config to the inbounds that exist
	// only there. Nil means the node follows the shared config.
	onlyTags map[string]bool
	// skipTags are inbounds that exist only on some node's own config. A node
	// following the shared config does not have them, so it must not be offered
	// for them: the config would point at a port nothing listens on.
	skipTags map[string]bool
}

// serves reports whether this node actually runs the inbound with this tag.
func (l ConfigLocation) serves(tag string) bool {
	if l.onlyTags != nil {
		return l.onlyTags[tag]
	}
	return !l.skipTags[tag]
}

// configLocations lists the nodes that run the shared config, in node order.
// Custom-config, disabled, limited and deleted nodes are left out. Any error
// (for example an older schema) yields no locations, which keeps the classic
// single-address links.
func (r Repository) configLocations(ctx context.Context) []ConfigLocation {
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(name, ''), COALESCE(NULLIF(TRIM(COALESCE(public_address, '')), ''), address), COALESCE(tunnel_address, '') FROM nodes
WHERE TRIM(COALESCE(address, '')) != ''
  AND LOWER(COALESCE(status, '')) NOT IN ('deleted', 'disabled', 'limited')
  AND LOWER(COALESCE(xray_config_mode, 'default')) <> 'custom'
ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var locations []ConfigLocation
	for rows.Next() {
		var name, address, tunnelAddress string
		if err := rows.Scan(&name, &address, &tunnelAddress); err != nil {
			return nil
		}
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		locations = append(locations, ConfigLocation{
			Name:          locationLabel(name, address),
			Address:       address,
			TunnelAddress: strings.TrimSpace(tunnelAddress),
		})
	}
	if rows.Err() != nil {
		return nil
	}
	return r.withNodeOnlyLocations(ctx, locations)
}

// withNodeOnlyLocations adds the nodes on their own Xray config, for the
// inbounds that exist only there.
//
// Such nodes used to be left out entirely, which is right for inbounds they
// share with the master config but wrong for ones only they run: those hosts
// then took the address of the first ordinary node, and every config pointed
// at a server where nothing listened on that port. T.Sin countries applied to
// a node are exactly this case.
//
// Anything unreadable here leaves the list as it was, so an older schema keeps
// the previous behaviour.
func (r Repository) withNodeOnlyLocations(ctx context.Context, locations []ConfigLocation) []ConfigLocation {
	var masterRaw []byte
	if err := r.db.QueryRowContext(ctx, `SELECT data FROM xray_config WHERE id = 1`).Scan(&masterRaw); err != nil {
		return locations
	}
	masterTags := xrayInboundTags(masterRaw)
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(name, ''), COALESCE(NULLIF(TRIM(COALESCE(public_address, '')), ''), address), COALESCE(tunnel_address, ''), xray_config FROM nodes
WHERE TRIM(COALESCE(address, '')) != ''
  AND LOWER(COALESCE(status, '')) NOT IN ('deleted', 'disabled', 'limited')
  AND LOWER(COALESCE(xray_config_mode, 'default')) = 'custom'
ORDER BY id`)
	if err != nil {
		return locations
	}
	defer rows.Close()
	var extra []ConfigLocation
	exclusive := map[string]bool{}
	for rows.Next() {
		var name, address, tunnelAddress string
		var raw []byte
		if err := rows.Scan(&name, &address, &tunnelAddress, &raw); err != nil {
			return locations
		}
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		only := map[string]bool{}
		for tag := range xrayInboundTags(raw) {
			if !masterTags[tag] {
				only[tag] = true
				exclusive[tag] = true
			}
		}
		if len(only) == 0 {
			continue
		}
		extra = append(extra, ConfigLocation{
			Name:          locationLabel(name, address),
			Address:       address,
			TunnelAddress: strings.TrimSpace(tunnelAddress),
			onlyTags:      only,
		})
	}
	if rows.Err() != nil || len(extra) == 0 {
		return locations
	}
	for i := range locations {
		locations[i].skipTags = exclusive
	}
	return append(locations, extra...)
}

func xrayInboundTags(raw []byte) map[string]bool {
	var config struct {
		Inbounds []struct {
			Tag string `json:"tag"`
		} `json:"inbounds"`
	}
	tags := map[string]bool{}
	if json.Unmarshal(raw, &config) != nil {
		return tags
	}
	for _, inbound := range config.Inbounds {
		if tag := strings.TrimSpace(inbound.Tag); tag != "" {
			tags[tag] = true
		}
	}
	return tags
}

// servingLocations keeps the nodes that run this inbound.
func servingLocations(locations []ConfigLocation, tag string) []ConfigLocation {
	out := make([]ConfigLocation, 0, len(locations))
	for _, location := range locations {
		if location.serves(tag) {
			out = append(out, location)
		}
	}
	return out
}

// hostUsesServerIP reports whether a host points at the node address
// placeholder; only those hosts are repeated per location.
func hostUsesServerIP(host Host) bool {
	return hostUsesPlaceholder(host, "{SERVER_IP}")
}

// hostUsesTunnel reports whether a host points at the tunnel placeholder. Such
// a host is repeated once per node that has a tunnel address, and skips the
// rest: a tunnel is bought per node, so a node without one has nowhere for
// that config to go.
func hostUsesTunnel(host Host) bool {
	return hostUsesPlaceholder(host, "{TUNNEL}")
}

func hostUsesPlaceholder(host Host, placeholder string) bool {
	if strings.Contains(strings.ToUpper(host.Address), placeholder) {
		return true
	}
	for _, option := range host.AddressOptions {
		if strings.Contains(strings.ToUpper(option), placeholder) {
			return true
		}
	}
	return false
}

// hostRemarkNamesLocation reports whether the admin already placed the
// location in the remark; otherwise it is prefixed automatically.
func hostRemarkNamesLocation(host Host) bool {
	upper := strings.ToUpper(host.Remark)
	return strings.Contains(upper, "{LOCATION}") || strings.Contains(upper, "{NODE_NAME}")
}

// locationLabel turns a node name into what users see, adding the country flag
// when the name mentions a place the panel knows -- in English, Finglish or
// Persian. A nameless node falls back to its address so it stays identifiable.
func locationLabel(name, address string) string {
	return countryflag.Label(name, address)
}
