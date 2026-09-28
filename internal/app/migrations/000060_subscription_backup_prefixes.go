package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000060_subscription_backup_prefixes.go", up000060SubscriptionBackupPrefixes, emptyDown)
}

// up000060SubscriptionBackupPrefixes adds the extra domains a subscription is
// also reachable on. The subscription route matches on path alone, so every
// domain pointed at the panel serves the same subscription; handing a user
// more than one link means a domain that gets blocked no longer cuts that
// user off, because their client already holds a working alternative.
func up000060SubscriptionBackupPrefixes(ctx context.Context, tx *sql.Tx) error {
	if err := addColumn(ctx, tx, activeDialect(), "subscription_settings", "subscription_backup_prefixes", "TEXT NOT NULL DEFAULT '[]'", "TEXT NULL"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE subscription_settings SET subscription_backup_prefixes = '[]' WHERE subscription_backup_prefixes IS NULL OR TRIM(subscription_backup_prefixes) = ''`)
	return err
}
