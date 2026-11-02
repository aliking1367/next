package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000073_telegram_mini_app_url.go", up000073TelegramMiniAppURL, emptyDown)
}

// up000073TelegramMiniAppURL stores the HTTPS address the Telegram bot opens
// its Mini App at. Empty keeps the Mini App buttons hidden.
func up000073TelegramMiniAppURL(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "telegram_settings", "mini_app_url",
		"TEXT NULL", "VARCHAR(512) NULL")
}
