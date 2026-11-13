package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000085_cdn_connect_address.go", up000085CDNConnectAddress, emptyDown)
}

// up000085CDNConnectAddress records where the countries' Cloudflare configs
// connect to while keeping the domain in use as their SNI and Host: a host
// name or IP that reaches Cloudflare by another way. Empty connects to the
// domain itself.
func up000085CDNConnectAddress(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "cdn_connect_address", "VARCHAR(255) NULL", "VARCHAR(255) NULL")
}
