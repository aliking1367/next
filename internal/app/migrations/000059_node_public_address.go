package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000059_node_public_address.go", up000059NodePublicAddress, emptyDown)
}

// up000059NodePublicAddress adds the address users see in their configs. The
// panel keeps reaching the node on its own address (usually an IP), while
// configs can advertise a domain, so the IP can change without reissuing
// every user's configs.
func up000059NodePublicAddress(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "nodes", "public_address", "VARCHAR(256) NULL", "VARCHAR(256) NULL")
}
