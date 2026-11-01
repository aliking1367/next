package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000072_subscription_info_lines.go", up000072SubscriptionInfoLines, emptyDown)
}

// up000072SubscriptionInfoLines adds the information lines shown at the top of
// a subscription: entries that carry no connection, only a name, so a user's
// app lists who the subscription belongs to, how much traffic and time is
// left, and a reminder to update it. Off until an admin turns it on.
func up000072SubscriptionInfoLines(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "subscription_settings", "subscription_info_enabled",
		"BOOLEAN NOT NULL DEFAULT 0", "TINYINT(1) NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "subscription_settings", "subscription_info_remarks",
		"TEXT NULL", "TEXT NULL")
}
