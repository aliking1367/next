package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000092_tsin_cloud_xhttp.go", up000092TSinCloudXHTTP, emptyDown)
}

// up000092TSinCloudXHTTP keeps the countries' Cloudflare XHTTP configs: the
// domain each country is named under, the server port its Origin Rule points
// at, where the configs connect to, and the countries that get one (JSON).
func up000092TSinCloudXHTTP(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	for _, column := range []struct{ name, sqlite, mysql string }{
		{"cloud_domain", "VARCHAR(255) NOT NULL DEFAULT ''", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"cloud_port", "INTEGER NOT NULL DEFAULT 0", "INT NOT NULL DEFAULT 0"},
		{"cloud_connect", "VARCHAR(255) NOT NULL DEFAULT ''", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"cloud_countries", "TEXT NULL", "TEXT NULL"},
	} {
		if err := addColumn(ctx, tx, dialect, "tsin_settings", column.name, column.sqlite, column.mysql); err != nil {
			return err
		}
	}
	return nil
}
