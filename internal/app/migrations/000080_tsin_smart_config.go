package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000080_tsin_smart_config.go", up000080TSinSmartConfig, emptyDown)
}

// up000080TSinSmartConfig adds the switch for the smart config, which pairs
// the direct (⚡) Best ping config with its Cloudflare one in Xray JSON
// subscriptions (user/smart_configs.go): 0 off, 1 in the JSON subscriptions
// apps already get, 2 also sends V2Box the JSON subscription. It starts off.
func up000080TSinSmartConfig(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "tsin_settings", "smart_config",
		"INTEGER NOT NULL DEFAULT 0", "INTEGER NOT NULL DEFAULT 0")
}
