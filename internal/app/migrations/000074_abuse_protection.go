package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000074_abuse_protection.go", up000074AbuseProtection, emptyDown)
}

// up000074AbuseProtection adds the switch that blocks the traffic hosting
// providers send abuse reports about (BitTorrent, outgoing mail on port 25)
// on every Xray the panel runs. It starts on.
func up000074AbuseProtection(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "settings", "abuse_protection",
		"INTEGER NOT NULL DEFAULT 1", "BOOLEAN NOT NULL DEFAULT TRUE")
}
