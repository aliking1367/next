package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000089_tunnel_usages.go", up000089TunnelUsages, emptyDown)
}

// up000089TunnelUsages keeps the traffic of each Iranian tunnel port (its
// gate inbound, and the emergency tunnel's inbounds) per half hour, so the
// dashboard can show a tunnel's usage by hour, day, week and month and its
// busiest hours. bucket is the half hour's start in Unix seconds, which reads
// the same in every database.
func up000089TunnelUsages(ctx context.Context, tx *sql.Tx) error {
	return createTable(ctx, tx, activeDialect(), "tunnel_usages", `
CREATE TABLE tunnel_usages (
	bucket BIGINT NOT NULL,
	tag VARCHAR(128) NOT NULL,
	uplink BIGINT NOT NULL DEFAULT 0,
	downlink BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (bucket, tag)
)`, `
CREATE TABLE tunnel_usages (
	bucket BIGINT NOT NULL,
	tag VARCHAR(128) NOT NULL,
	uplink BIGINT NOT NULL DEFAULT 0,
	downlink BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (bucket, tag)
)`)
}
