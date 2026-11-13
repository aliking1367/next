package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000086_tsin_tunnel_ports.go", up000086TSinTunnelPorts, emptyDown)
}

// up000086TSinTunnelPorts records, per tunnel port the countries' tunnel
// configs use, where users reach it: a JSON list of the port on the
// countries' server, the address the tunnel seller gave and the port on the
// tunnel's side.
func up000086TSinTunnelPorts(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "tunnel_ports", "TEXT NULL", "TEXT NULL")
}
