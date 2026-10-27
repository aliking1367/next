package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000068_tsin_settings.go", up000068TSinSettings, emptyDown)
}

// up000068TSinSettings keeps the T.Sin account the panel was given: the token,
// the datacenter it registered with and the countries it serves.
//
// The token is stored because the panel needs it after the request that
// supplied it has ended, to read the account's usage; it is never returned by
// the API. Registered records that the tunnel profile was already fetched for
// this token and datacenter, so applying again does not register a second time.
func up000068TSinSettings(ctx context.Context, tx *sql.Tx) error {
	return createTable(ctx, tx, activeDialect(), "tsin_settings", `
CREATE TABLE tsin_settings (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	token TEXT NOT NULL DEFAULT '',
	dc VARCHAR(16) NOT NULL DEFAULT 'dc1',
	countries TEXT NOT NULL DEFAULT '[]',
	registered_dc VARCHAR(16) NOT NULL DEFAULT '',
	registered_token_hash VARCHAR(64) NOT NULL DEFAULT '',
	last_applied_at DATETIME NULL,
	last_detail TEXT NOT NULL DEFAULT ''
)`, `
CREATE TABLE tsin_settings (
	id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
	token TEXT NULL,
	dc VARCHAR(16) NOT NULL DEFAULT 'dc1',
	countries TEXT NULL,
	registered_dc VARCHAR(16) NOT NULL DEFAULT '',
	registered_token_hash VARCHAR(64) NOT NULL DEFAULT '',
	last_applied_at DATETIME(6) NULL,
	last_detail TEXT NULL
)`)
}
