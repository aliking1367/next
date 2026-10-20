package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000066_node_tunnel_address.go", up000066NodeTunnelAddress, emptyDown)
}

// up000066NodeTunnelAddress records the address users reach a node through when
// it is fronted by a tunnel.
//
// A tunnel is bought per node and its address cannot be derived from anything
// the panel knows, so until now every tunnel needed its own host row written by
// hand, and a host row is easy to forget when a node is added or a tunnel is
// moved. Keeping it on the node lets one host row serve every node that has
// one, the same way a host pointing at {SERVER_IP} already serves every node.
//
// It is separate from public_address because the two coexist: a node is
// commonly offered both directly and through its tunnel, and one field cannot
// name both.
func up000066NodeTunnelAddress(ctx context.Context, tx *sql.Tx) error {
	if err := addColumn(ctx, tx, activeDialect(), "nodes", "tunnel_address",
		"VARCHAR(253) NOT NULL DEFAULT ''", "VARCHAR(253) NULL"); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE nodes SET tunnel_address = '' WHERE tunnel_address IS NULL`)
	return err
}
