package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000087_tsin_vision.go", up000087TSinVision, emptyDown)
}

// up000087TSinVision records which countries also get a TCP Vision config
// (a JSON list of names), the site those configs borrow for REALITY, and
// whether the countries' Cloudflare configs are kept out of subscriptions.
func up000087TSinVision(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "vision_countries", "TEXT NULL", "TEXT NULL"); err != nil {
		return err
	}
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "vision_sni", "TEXT NULL", "TEXT NULL"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "tsin_settings", "cdn_hidden", "INTEGER NOT NULL DEFAULT 0", "BOOLEAN NOT NULL DEFAULT FALSE")
}
