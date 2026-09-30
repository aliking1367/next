package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000063_node_install_tokens.go", up000063NodeInstallTokens, emptyDown)
}

// up000063NodeInstallTokens adds the one-time tokens a node server uses to
// fetch its own install bundle. Pasting a PEM bundle into a terminal is the
// step that most often fails, so the panel hands out a short-lived token
// instead and the node collects the bundle over HTTPS by itself.
//
// Only a hash of the token is stored, the same way admin API keys are kept:
// the token is shown once, and a copy of this table is not enough to install
// a node.
func up000063NodeInstallTokens(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := createTable(ctx, tx, dialect, "node_install_tokens", `
CREATE TABLE node_install_tokens (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	node_id INTEGER NOT NULL,
	token_hash VARCHAR(128) NOT NULL UNIQUE,
	created_at DATETIME NOT NULL,
	expires_at DATETIME NOT NULL,
	used_at DATETIME NULL
)`, `
CREATE TABLE node_install_tokens (
	id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
	node_id BIGINT NOT NULL,
	token_hash VARCHAR(128) NOT NULL,
	created_at DATETIME(6) NOT NULL,
	expires_at DATETIME(6) NOT NULL,
	used_at DATETIME(6) NULL,
	UNIQUE KEY uq_node_install_tokens_hash (token_hash),
	KEY ix_node_install_tokens_node_id (node_id)
)`); err != nil {
		return err
	}
	if dialect == "sqlite" {
		if _, err := CreateIndexIfMissing(ctx, tx, dialect, "node_install_tokens", "ix_node_install_tokens_node_id", []string{"node_id"}, false); err != nil {
			return err
		}
	}
	return nil
}
