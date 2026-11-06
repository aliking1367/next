package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000076_tsin_direct_address.go", up000076TSinDirectAddress, emptyDown)
}

// up000076TSinDirectAddress stores the extra IP the direct T.Sin configs
// point at, so the server's main IP never appears in them. Empty uses the
// server's own address.
func up000076TSinDirectAddress(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "direct_address",
		"TEXT NULL", "VARCHAR(255) NULL")
}
