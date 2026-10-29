package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000070_tsin_transport.go", up000070TSinTransport, emptyDown)
}

// up000070TSinTransport records which transport the T.Sin country inbounds
// use. Existing setups keep TCP, which is what they were built with: changing
// it silently would rebuild every country inbound on the next apply and move
// every user's link to a new port.
func up000070TSinTransport(ctx context.Context, tx *sql.Tx) error {
	if err := addColumn(ctx, tx, activeDialect(), "tsin_settings", "transport",
		"VARCHAR(16) NOT NULL DEFAULT 'tcp'", "VARCHAR(16) NOT NULL DEFAULT 'tcp'"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE tsin_settings SET transport = 'tcp' WHERE transport IS NULL OR transport = ''`)
	return err
}
