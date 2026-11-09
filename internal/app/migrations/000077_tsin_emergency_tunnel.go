package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000077_tsin_emergency_tunnel.go", up000077TSinEmergencyTunnel, emptyDown)
}

// up000077TSinEmergencyTunnel stores the emergency tunnel for when T.Sin is
// down: the Iranian tunnel's address and ports, and whether it is on. Off
// until an admin turns it on.
func up000077TSinEmergencyTunnel(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	for _, column := range []struct{ name, sqlite, mysql string }{
		{"emergency_address", "TEXT NULL", "VARCHAR(255) NULL"},
		{"emergency_vmess_port", "INTEGER NOT NULL DEFAULT 0", "INT NOT NULL DEFAULT 0"},
		{"emergency_ss_port", "INTEGER NOT NULL DEFAULT 0", "INT NOT NULL DEFAULT 0"},
		{"emergency_enabled", "INTEGER NOT NULL DEFAULT 0", "BOOLEAN NOT NULL DEFAULT FALSE"},
	} {
		if err := addColumn(ctx, tx, dialect, "tsin_settings", column.name, column.sqlite, column.mysql); err != nil {
			return err
		}
	}
	return nil
}
