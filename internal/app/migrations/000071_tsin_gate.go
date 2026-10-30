package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000071_tsin_gate.go", up000071TSinGate, emptyDown)
}

// up000071TSinGate stores where path mode is reached: the Cloudflare-proxied
// domain every country config uses, and the origin port its gate listens on.
func up000071TSinGate(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "gate_host",
		"VARCHAR(253) NOT NULL DEFAULT ''", "VARCHAR(253) NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "tsin_settings", "gate_port",
		"INTEGER NOT NULL DEFAULT 2087", "INT NOT NULL DEFAULT 2087")
}
