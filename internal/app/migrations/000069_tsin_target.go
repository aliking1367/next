package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000069_tsin_target.go", up000069TSinTarget, emptyDown)
}

// up000069TSinTarget records which Xray config the T.Sin countries were
// applied to: the panel's own ("master") or one node ("node:<id>").
//
// The tunnel their exits sit behind exists on one machine, so the countries
// must be served by the Xray on that machine. Keeping the target lets the
// dashboard show where it is, and lets applying again move it deliberately
// instead of leaving a copy behind on the old one.
func up000069TSinTarget(ctx context.Context, tx *sql.Tx) error {
	if err := addColumn(ctx, tx, activeDialect(), "tsin_settings", "target",
		"VARCHAR(32) NOT NULL DEFAULT 'master'", "VARCHAR(32) NOT NULL DEFAULT 'master'"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE tsin_settings SET target = 'master' WHERE target IS NULL OR target = ''`)
	return err
}
