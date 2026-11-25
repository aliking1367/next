package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000095_tsin_tls.go", up000095TSinTLS, emptyDown)
}

// up000095TSinTLS records which countries also get a VLESS + TLS config (a
// JSON list of names), the server name those configs use and carry a
// certificate for, and the country that takes port 443.
func up000095TSinTLS(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "tls_countries", "TEXT NULL", "TEXT NULL"); err != nil {
		return err
	}
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "tls_server_name", "VARCHAR(255) NULL", "TEXT NULL"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "tsin_settings", "tls_main_country", "VARCHAR(128) NULL", "TEXT NULL")
}
