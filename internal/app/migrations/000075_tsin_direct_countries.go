package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000075_tsin_direct_countries.go", up000075TSinDirectCountries, emptyDown)
}

// up000075TSinDirectCountries stores which T.Sin countries also get a direct
// config on the server's own IP beside their Cloudflare one. Empty until an
// admin chooses, so nothing changes on update.
func up000075TSinDirectCountries(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "direct_countries",
		"TEXT NULL", "TEXT NULL")
}
