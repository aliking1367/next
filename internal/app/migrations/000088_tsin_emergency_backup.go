package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000088_tsin_emergency_backup.go", up000088TSinEmergencyBackup, emptyDown)
}

// up000088TSinEmergencyBackup records which other configs of the countries'
// service (a JSON list of host ids) are shown only while the emergency tunnel
// is on, so the backups for a T.Sin outage come and go with its switch.
func up000088TSinEmergencyBackup(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "emergency_backup_hosts", "TEXT NULL", "TEXT NULL")
}
