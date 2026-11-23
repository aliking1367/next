package xrayconfig

import (
	"context"
	"strings"
)

// TSinTunnelTarget is where one country's tunnel config points: the Iranian
// tunnel's address and the port users reach it on.
type TSinTunnelTarget struct {
	Address string
	Port    int
}

// SetTSinAddresses points the countries' configs at new addresses without
// touching any inbound: the ⚡ and 💎 configs at directAddress when it is
// given (a comma list is fine, each subscription then picks one of them at
// random; empty is the server's own IP), and each country in tunnels at its
// tunnel. Only host
// rows change, so Xray is not reloaded and nobody is disconnected; users get
// the new addresses when they update their subscription. It returns how many
// configs changed.
func (r Repository) SetTSinAddresses(ctx context.Context, directAddress *string, tunnels map[string]TSinTunnelTarget) (int64, error) {
	var changed int64
	if directAddress != nil {
		address := strings.TrimSpace(*directAddress)
		if address == "" {
			address = "{SERVER_IP}"
		}
		result, err := r.db.ExecContext(ctx, `UPDATE hosts SET address = ?
WHERE inbound_tag LIKE 'tsin-%' AND inbound_tag NOT LIKE ? AND (inbound_tag LIKE ? OR inbound_tag LIKE ?) AND address <> ?`,
			address, TSinLabPrefix+"%", "%"+TSinVisionSuffix, "%"+TSinDirectSuffix, address)
		if err != nil {
			return changed, err
		}
		if n, err := result.RowsAffected(); err == nil {
			changed += n
		}
	}
	for country, target := range tunnels {
		address := strings.TrimSpace(target.Address)
		if address == "" || target.Port <= 0 {
			continue
		}
		result, err := r.db.ExecContext(ctx, `UPDATE hosts SET address = ?, port = ? WHERE inbound_tag = ? AND NOT (address = ? AND port <=> ?)`,
			address, target.Port, TSinTunnelInboundTag(country), address, target.Port)
		if err != nil {
			return changed, err
		}
		if n, err := result.RowsAffected(); err == nil {
			changed += n
		}
	}
	return changed, nil
}
