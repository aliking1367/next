package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000064_cdn_automation.go", up000064CDNAutomation, emptyDown)
}

// up000064CDNAutomation stores the one set of answers the panel needs to keep
// every node's CDN hostname correct by itself: the zone the hostnames hang
// off, the inbound they belong to, and a Cloudflare token.
//
// Doing this per node by hand is several steps that each have to be right at
// the same time -- a proxied DNS record, a host row copied from a working one,
// and a rule telling Cloudflare not to challenge a client that cannot answer a
// challenge. Keeping the answers here lets adding a node, renaming one, or
// swapping a filtered server for a fresh one be one action instead of six.
//
// The token is stored because automation needs it after the request that
// supplied it has ended; it is never read back out over the API. It only ever
// needs Zone:Read, DNS:Edit and WAF:Edit on the single zone.
func up000064CDNAutomation(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := createTable(ctx, tx, dialect, "cdn_automation", `
CREATE TABLE cdn_automation (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	enabled BOOLEAN NOT NULL DEFAULT 0,
	domain_suffix VARCHAR(253) NOT NULL DEFAULT '',
	inbound_tag VARCHAR(190) NOT NULL DEFAULT '',
	cloudflare_token TEXT NOT NULL DEFAULT '',
	manage_security_rule BOOLEAN NOT NULL DEFAULT 1,
	remove_records_on_delete BOOLEAN NOT NULL DEFAULT 1,
	last_sync_at DATETIME NULL,
	last_sync_detail TEXT NOT NULL DEFAULT ''
)`, `
CREATE TABLE cdn_automation (
	id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
	enabled TINYINT(1) NOT NULL DEFAULT 0,
	domain_suffix VARCHAR(253) NOT NULL DEFAULT '',
	inbound_tag VARCHAR(190) NOT NULL DEFAULT '',
	cloudflare_token TEXT NULL,
	manage_security_rule TINYINT(1) NOT NULL DEFAULT 1,
	remove_records_on_delete TINYINT(1) NOT NULL DEFAULT 1,
	last_sync_at DATETIME(6) NULL,
	last_sync_detail TEXT NULL
)`); err != nil {
		return err
	}
	// The hostname a node currently owns is kept on the node itself. Without
	// it a rename would silently orphan the previous record: the old hostname
	// would keep resolving to the node while no longer appearing anywhere the
	// panel looks, so it could never be cleaned up or corrected.
	if err := addColumn(ctx, tx, dialect, "nodes", "cdn_hostname",
		"VARCHAR(253) NOT NULL DEFAULT ''", "VARCHAR(253) NULL"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE nodes SET cdn_hostname = '' WHERE cdn_hostname IS NULL`); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM cdn_automation`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO cdn_automation
(enabled, domain_suffix, inbound_tag, cloudflare_token, manage_security_rule, remove_records_on_delete, last_sync_detail)
VALUES (0, '', '', '', 0, 1, '')`)
	return err
}
