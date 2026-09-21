package migrations

import (
	"context"
	"database/sql"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000058_default_client_routing_rules.go", up000058DefaultClientRoutingRules, emptyDown)
}

// defaultClientRoutingRules58 is a frozen copy of the panel's default client
// rules at the time of this migration.
const defaultClientRoutingRules58 = `[{"pattern":"^([Cc]lash-verge|[Cc]lash[-\\.]?[Mm]eta|[Ff][Ll][Cc]lash|[Mm]ihomo)","result":"clash-meta"},{"pattern":"(?i)^clash\\s*mi|(?i)^clashmi","result":"clash-mi"},{"pattern":"^([Cc]lash|[Ss]tash)","result":"clash"},{"pattern":"(?i)^karing","result":"karing"},{"pattern":"(?i)^hiddifynextx?","result":"hiddify"},{"pattern":"^(SFA|SFI|SFM|SFT)","result":"sing-box"},{"pattern":"(?i)^v2raytun","result":"v2raytun"},{"pattern":"(?i)^shadowrocket","result":"shadowrocket"},{"pattern":"(?i)^(nekobox|nekoboxforandroid)","result":"nekobox"},{"pattern":"(?i)^passwall","result":"passwall"},{"pattern":"(?i)^thron(e)?","result":"throne"},{"pattern":"^(SS|SSR|SSD|SSS|Outline|Shadowsocks|SSconf)","result":"outline"},{"pattern":"^v2rayN/(?:6\\.[4-9]\\d*|[7-9]\\.\\d+|[1-9]\\d{1,}\\.\\d+)","result":"v2ray-json"},{"pattern":"(?i)^v2rayng/\\d+\\.\\d+","result":"v2ray-json"},{"pattern":"^Happ/(?:1\\.63\\.[1-9]|1\\.6[4-9]\\d*|1\\.[7-9]\\d*|[2-9]\\.\\d+)","result":"happ"},{"pattern":"(?i)^incy","result":"incy"},{"pattern":"^Streisand","result":"v2ray-json"}]`

// up000058DefaultClientRoutingRules fills the client rules on panels upgraded
// through 000056/000057. Those migrations added the rules column empty and
// dropped the per-app JSON toggles, so every app (Clash, sing-box, Hiddify...)
// silently fell back to the plain v2ray format and the rules page looked empty
// until "reset to default" was pressed. Only empty rules are touched, and only
// once, so rules an admin configured (or cleared later) are kept.
func up000058DefaultClientRoutingRules(ctx context.Context, tx *sql.Tx) error {
	dialect := NormalizeDialect(activeDialect())
	exists, err := HasColumn(ctx, tx, dialect, "subscription_settings", "client_routing_rules")
	if err != nil || !exists {
		return err
	}
	empty := `client_routing_rules IS NULL OR TRIM(client_routing_rules) IN ('', '[]')`
	if dialect == "mysql" {
		empty = `client_routing_rules IS NULL OR JSON_LENGTH(client_routing_rules) = 0`
	}
	_, err = tx.ExecContext(ctx, `UPDATE subscription_settings SET client_routing_rules = ? WHERE `+empty, defaultClientRoutingRules58)
	return err
}
