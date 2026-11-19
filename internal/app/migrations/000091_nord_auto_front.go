package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000091_nord_auto_front.go", up000091NordAutoFront, emptyDown)
}

// up000091NordAutoFront keeps the NordVPN configs' shared port: when set (443),
// every location is reached on that one port of the entry address, told apart
// by its own SNI, and its config listens only on the target's loopback.
func up000091NordAutoFront(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "nord_auto_settings", "front_port", "INTEGER NOT NULL DEFAULT 0", "INT NOT NULL DEFAULT 0")
}
