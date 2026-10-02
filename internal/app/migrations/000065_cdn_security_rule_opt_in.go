package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000065_cdn_security_rule_opt_in.go", up000065CDNSecurityRuleOptIn, emptyDown)
}

// up000065CDNSecurityRuleOptIn turns the security-rule step off on panels that
// already exist.
//
// The column arrived in 000064 defaulting to on, before anything read it. Now
// that it does something -- the panel writes a Cloudflare rule that skips
// Cloudflare's checks for the CDN hostnames -- an upgrade must not switch that
// on for an admin who never asked. Turning a protection off is a decision to
// make, not to inherit from a default, so every existing row is set to off and
// the dashboard asks.
//
// New panels keep the column default, which the create path sets explicitly.
func up000065CDNSecurityRuleOptIn(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `UPDATE cdn_automation SET manage_security_rule = 0`)
	return err
}
