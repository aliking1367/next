package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000090_nord_auto.go", up000090NordAuto, emptyDown)
}

// up000090NordAuto keeps the settings of the NordVPN auto configs: when on,
// every NordVPN location added as an outbound on the target gets its own
// config (a Vision inbound behind the entry address) on the chosen service.
// It is one row, separate from T.Sin. managed maps each location's outbound
// tag to the inbound made for it, as JSON.
func up000090NordAuto(ctx context.Context, tx *sql.Tx) error {
	return createTable(ctx, tx, activeDialect(), "nord_auto_settings", `
CREATE TABLE nord_auto_settings (
	id INTEGER NOT NULL PRIMARY KEY,
	enabled BOOLEAN NOT NULL DEFAULT 0,
	target VARCHAR(64) NOT NULL DEFAULT '',
	address VARCHAR(255) NOT NULL DEFAULT '',
	sni VARCHAR(255) NOT NULL DEFAULT '',
	service_id BIGINT NULL,
	ai_outbound VARCHAR(255) NOT NULL DEFAULT '',
	managed TEXT NULL
)`, `
CREATE TABLE nord_auto_settings (
	id INT NOT NULL PRIMARY KEY,
	enabled BOOLEAN NOT NULL DEFAULT 0,
	target VARCHAR(64) NOT NULL DEFAULT '',
	address VARCHAR(255) NOT NULL DEFAULT '',
	sni VARCHAR(255) NOT NULL DEFAULT '',
	service_id BIGINT NULL,
	ai_outbound VARCHAR(255) NOT NULL DEFAULT '',
	managed TEXT NULL
)`)
}
