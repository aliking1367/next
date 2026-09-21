package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestDefaultClientRoutingRulesFillsOnlyEmptyRules(t *testing.T) {
	var rules []map[string]string
	if err := json.Unmarshal([]byte(defaultClientRoutingRules58), &rules); err != nil || len(rules) == 0 {
		t.Fatalf("default rules must be a non-empty JSON array: %v", err)
	}

	for _, test := range []struct {
		name   string
		stored any
		want   string
	}{
		{name: "empty array from 000056", stored: "[]", want: defaultClientRoutingRules58},
		{name: "blank", stored: " ", want: defaultClientRoutingRules58},
		{name: "null", stored: nil, want: defaultClientRoutingRules58},
		{name: "admin rules are kept", stored: `[{"pattern":"^MyApp","result":"clash"}]`, want: `[{"pattern":"^MyApp","result":"clash"}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "rules.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`CREATE TABLE subscription_settings (id INTEGER PRIMARY KEY, client_routing_rules TEXT NULL)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO subscription_settings (id, client_routing_rules) VALUES (1, ?)`, test.stored); err != nil {
				t.Fatal(err)
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := up000058DefaultClientRoutingRules(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := db.QueryRow(`SELECT client_routing_rules FROM subscription_settings WHERE id = 1`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("rules = %q, want %q", got, test.want)
			}
		})
	}
}
