package user

import (
	"context"
	"strings"

	"github.com/aliking1367/next/internal/app/countryflag"
)

// ConfigLocation is one node that serves the shared Xray config. Hosts whose
// address is {SERVER_IP} are offered once per location ("multi-location"),
// so a subscription lists every country the panel runs nodes in.
type ConfigLocation struct {
	Name    string
	Address string
}

// configLocations lists the nodes that run the shared config, in node order.
// Custom-config, disabled, limited and deleted nodes are left out. Any error
// (for example an older schema) yields no locations, which keeps the classic
// single-address links.
func (r Repository) configLocations(ctx context.Context) []ConfigLocation {
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(name, ''), COALESCE(NULLIF(TRIM(COALESCE(public_address, '')), ''), address) FROM nodes
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
		var name, address string
		if err := rows.Scan(&name, &address); err != nil {
			return nil
		}
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		locations = append(locations, ConfigLocation{Name: locationLabel(name, address), Address: address})
	}
	if rows.Err() != nil {
		return nil
	}
	return locations
}

// hostUsesServerIP reports whether a host points at the node address
// placeholder; only those hosts are repeated per location.
func hostUsesServerIP(host Host) bool {
	if strings.Contains(strings.ToUpper(host.Address), "{SERVER_IP}") {
		return true
	}
	for _, option := range host.AddressOptions {
		if strings.Contains(strings.ToUpper(option), "{SERVER_IP}") {
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
