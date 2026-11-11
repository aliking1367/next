package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000083_emergency_mci.go", up000083EmergencyMCI, emptyDown)
}

// up000083EmergencyMCI lets the emergency tunnel be shown only to users who
// update their subscription from Hamrah Aval (MCI), whose network the tunnel
// is bought for:
//   - tsin_settings.emergency_audience is "all" (the default) or "mci";
//   - tsin_settings.operator_ranges caches MCI's and Iran's IP ranges, read
//     from RIPE once a day, so a restart does not need RIPE to answer;
//   - user_subscription_access.mci_seen_at is when the user last updated
//     from MCI, so an update made through a VPN keeps the tunnel config.
func up000083EmergencyMCI(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "emergency_audience", "VARCHAR(8) NULL", "VARCHAR(8) NULL"); err != nil {
		return err
	}
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "operator_ranges", "TEXT NULL", "MEDIUMTEXT NULL"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "user_subscription_access", "mci_seen_at", "DATETIME NULL", "DATETIME(6) NULL")
}
