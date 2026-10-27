package migrations

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/pressly/goose/v3"
)

func init() {
	goose.AddNamedMigrationContext("000067_v2rayng_plain_links.go", up000067V2rayNGPlainLinks, emptyDown)
}

// plainLinkPatterns67 are the default v2rayN and v2rayNG rules exactly as
// 000058 wrote them.
var plainLinkPatterns67 = map[string]bool{
	`^v2rayN/(?:6\.[4-9]\d*|[7-9]\.\d+|[1-9]\d{1,}\.\d+)`: true,
	`(?i)^v2rayng/\d+\.\d+`:                               true,
}

// up000067V2rayNGPlainLinks sends v2rayN and v2rayNG plain share links again
// instead of full Xray JSON.
//
// Both apps import a JSON subscription as "Custom" profiles: v2rayNG lists them
// with the user's UUID as the address and port 0, and in practice users could
// not connect with them at all, while the same subscription as links worked
// immediately. Links are what these apps are built around, and every protocol
// the panel offers has one.
//
// Only rules still carrying the 000058 default are changed. An admin who chose
// JSON for these apps on purpose, or wrote their own pattern, keeps it.
func up000067V2rayNGPlainLinks(ctx context.Context, tx *sql.Tx) error {
	dialect := NormalizeDialect(activeDialect())
	exists, err := HasColumn(ctx, tx, dialect, "subscription_settings", "client_routing_rules")
	if err != nil || !exists {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, client_routing_rules FROM subscription_settings`)
	if err != nil {
		return err
	}
	updates := map[int64]string{}
	for rows.Next() {
		var id int64
		var stored sql.NullString
		if err := rows.Scan(&id, &stored); err != nil {
			rows.Close()
			return err
		}
		if updated, changed := plainLinksForV2rayNG(stored.String); changed {
			updates[id] = updated
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, rules := range updates {
		if _, err := tx.ExecContext(ctx, `UPDATE subscription_settings SET client_routing_rules = ? WHERE id = ?`, rules, id); err != nil {
			return err
		}
	}
	return nil
}

func plainLinksForV2rayNG(stored string) (string, bool) {
	if strings.TrimSpace(stored) == "" {
		return "", false
	}
	var rules []map[string]any
	if err := json.Unmarshal([]byte(stored), &rules); err != nil {
		// Rules the panel cannot read are left exactly as they are.
		return "", false
	}
	changed := false
	for _, rule := range rules {
		pattern, _ := rule["pattern"].(string)
		result, _ := rule["result"].(string)
		if plainLinkPatterns67[pattern] && result == "v2ray-json" {
			rule["result"] = "v2ray"
			changed = true
		}
	}
	if !changed {
		return "", false
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	// Patterns are regular expressions; "<" or "&" escaped to < would
	// still parse, but would read as noise on the rules page.
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(rules); err != nil {
		return "", false
	}
	return strings.TrimSpace(buf.String()), true
}
