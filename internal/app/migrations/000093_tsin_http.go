package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000093_tsin_http.go", up000093TSinHTTP, emptyDown)
}

// up000093TSinHTTP records which countries also get a VMess config with an
// HTTP header (a JSON list of names) and the site those configs name.
func up000093TSinHTTP(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "http_countries", "TEXT NULL", "TEXT NULL"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "tsin_settings", "http_host", "TEXT NULL", "TEXT NULL")
}
