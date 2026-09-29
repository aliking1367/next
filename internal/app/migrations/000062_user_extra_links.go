package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000062_user_extra_links.go", up000062UserExtraLinks, emptyDown)
}

// up000062UserExtraLinks adds per-user configs the admin supplies by hand.
// When every config the panel serves is blocked, a user's subscription is
// still fetched but has nothing usable in it; an admin who has found a
// working config elsewhere can put it here and reach that one user without
// touching any inbound, host or service.
func up000062UserExtraLinks(ctx context.Context, tx *sql.Tx) error {
	return addColumn(ctx, tx, activeDialect(), "users", "extra_links", "TEXT NULL", "TEXT NULL")
}
