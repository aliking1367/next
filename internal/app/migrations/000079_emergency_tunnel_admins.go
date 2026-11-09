package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000079_emergency_tunnel_admins.go", up000079EmergencyTunnelAdmins, emptyDown)
}

// up000079EmergencyTunnelAdmins lets a hidden host be shown to the users of
// chosen admins only: an admin allowed to turn the emergency tunnel on does
// so for their own users, while the owner's switch shows it to everyone.
// hosts.enabled_admins lists those admins; tsin_settings.emergency_admins
// keeps who turned the emergency tunnel on.
func up000079EmergencyTunnelAdmins(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "hosts", "enabled_admins", "TEXT NULL", "TEXT NULL"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "tsin_settings", "emergency_admins", "TEXT NULL", "TEXT NULL")
}
