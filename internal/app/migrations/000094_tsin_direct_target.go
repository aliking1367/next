package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000094_tsin_direct_target.go", up000094TSinDirectTarget, emptyDown)
}

// up000094TSinDirectTarget records the server the direct configs (⚡, 💎 and
// HTTP) run on when it is not the countries' own: a second server with a
// second T.Sin licence of its own. Empty keeps them with the countries.
func up000094TSinDirectTarget(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "direct_target", "VARCHAR(64) NULL", "TEXT NULL")
}
