package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000081_cdn_domains.go", up000081CDNDomains, emptyDown)
}

// up000081CDNDomains keeps the pool of Cloudflare domains the countries can be
// served on. One of them is in use (tsin_settings.gate_host); the others wait
// set up and checked, so a blocked domain is swapped in minutes. The last
// check from outside Iran and from Iran's test servers is kept per domain.
func up000081CDNDomains(ctx context.Context, tx *sql.Tx) error {
	return createTable(ctx, tx, activeDialect(), "cdn_domains", `
CREATE TABLE cdn_domains (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	domain VARCHAR(253) NOT NULL UNIQUE,
	note TEXT NOT NULL DEFAULT '',
	created_at DATETIME NULL,
	checked_at DATETIME NULL,
	check_state VARCHAR(16) NOT NULL DEFAULT '',
	check_reason VARCHAR(32) NOT NULL DEFAULT '',
	check_status INTEGER NOT NULL DEFAULT 0,
	check_detail TEXT NOT NULL DEFAULT '',
	iran_checked_at DATETIME NULL,
	iran_open INTEGER NOT NULL DEFAULT 0,
	iran_total INTEGER NOT NULL DEFAULT 0,
	iran_error TEXT NOT NULL DEFAULT ''
)`, `
CREATE TABLE cdn_domains (
	id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
	domain VARCHAR(253) NOT NULL,
	note TEXT NULL,
	created_at DATETIME(6) NULL,
	checked_at DATETIME(6) NULL,
	check_state VARCHAR(16) NOT NULL DEFAULT '',
	check_reason VARCHAR(32) NOT NULL DEFAULT '',
	check_status INT NOT NULL DEFAULT 0,
	check_detail TEXT NULL,
	iran_checked_at DATETIME(6) NULL,
	iran_open INT NOT NULL DEFAULT 0,
	iran_total INT NOT NULL DEFAULT 0,
	iran_error TEXT NULL,
	UNIQUE KEY uq_cdn_domains_domain (domain)
)`)
}
