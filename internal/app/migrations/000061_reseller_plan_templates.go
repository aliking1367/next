package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000061_reseller_plan_templates.go", up000061ResellerPlanTemplates, emptyDown)
}

// up000061ResellerPlanTemplates adds the plan presets that Marzban calls user
// templates. Reseller bots read them to offer plans and then create the user
// from one, so a panel without this endpoint leaves such a bot with no plans
// to sell. A template optionally points at a service, which is how this panel
// expresses "which hosts the user gets".
func up000061ResellerPlanTemplates(ctx context.Context, tx *sql.Tx) error {
	dialect := activeDialect()
	if err := createTable(ctx, tx, dialect, "reseller_plan_templates", `
CREATE TABLE reseller_plan_templates (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name VARCHAR(64) NOT NULL,
	data_limit BIGINT NOT NULL DEFAULT 0,
	expire_duration BIGINT NOT NULL DEFAULT 0,
	username_prefix VARCHAR(20) NULL,
	username_suffix VARCHAR(20) NULL,
	service_id INTEGER NULL,
	inbounds TEXT NULL,
	created_at DATETIME NULL,
	updated_at DATETIME NULL
)`, `
CREATE TABLE reseller_plan_templates (
	id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
	name VARCHAR(64) NOT NULL,
	data_limit BIGINT NOT NULL DEFAULT 0,
	expire_duration BIGINT NOT NULL DEFAULT 0,
	username_prefix VARCHAR(20) NULL,
	username_suffix VARCHAR(20) NULL,
	service_id BIGINT NULL,
	inbounds TEXT NULL,
	created_at DATETIME(6) NULL,
	updated_at DATETIME(6) NULL,
	KEY ix_reseller_plan_templates_service_id (service_id)
)`); err != nil {
		return err
	}
	if dialect == "sqlite" {
		if _, err := CreateIndexIfMissing(ctx, tx, dialect, "reseller_plan_templates", "ix_reseller_plan_templates_service_id", []string{"service_id"}, false); err != nil {
			return err
		}
	}
	return nil
}
