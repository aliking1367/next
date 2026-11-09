package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000078_tsin_emergency_protocols.go", up000078TSinEmergencyProtocols, emptyDown)
}

// up000078TSinEmergencyProtocols stores which emergency configs are shown:
// both, or one of them for an admin who bought a single tunnel port, since
// one Iranian port forwards to one port on the server.
func up000078TSinEmergencyProtocols(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "emergency_protocols",
		"TEXT NULL", "VARCHAR(16) NULL")
}
