package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000096_host_vless_route.go", up000096HostVLESSRoute, emptyDown)
}

// up000096HostVLESSRoute lets a host carry a VLESS route number. Its links
// put that number in the user's UUID (bytes 7 and 8), and the server's
// routing sends the connection by it: one inbound on port 443 then serves
// every T.Sin country over VLESS + TLS.
func up000096HostVLESSRoute(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "hosts", "vless_route", "INTEGER NULL", "INT NULL")
}
