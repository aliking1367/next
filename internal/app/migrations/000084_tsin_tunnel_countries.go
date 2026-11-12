package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000084_tsin_tunnel_countries.go", up000084TSinTunnelCountries, emptyDown)
}

// up000084TSinTunnelCountries records which countries also get a config
// through the admin's Iranian tunnel, and on which tunnel port: a JSON object
// from the country's name in lower case to the port.
func up000084TSinTunnelCountries(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "tunnel_countries", "TEXT NULL", "TEXT NULL")
}
