package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000082_tsin_fastly.go", up000082TSinFastly, emptyDown)
}

// up000082TSinFastly keeps the Fastly service's domain and the countries that
// also get a config through Fastly, a second CDN beside Cloudflare
// (xrayconfig/tsin_fastly.go). Both start empty: nothing changes until an
// admin sets them and presses Apply.
func up000082TSinFastly(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := addColumn(ctx, tx, dialect, "tsin_settings", "fastly_host", "TEXT NULL", "TEXT NULL"); err != nil {
		return err
	}
	return addColumn(ctx, tx, dialect, "tsin_settings", "fastly_countries", "TEXT NULL", "TEXT NULL")
}
