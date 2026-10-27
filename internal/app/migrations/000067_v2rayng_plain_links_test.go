package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestV2rayNGPlainLinksChangesOnlyTheDefaultRules(t *testing.T) {
	for _, test := range []struct {
		name   string
		stored any
		want   map[string]string
	}{
		{
			name:   "defaults from 000058",
			stored: defaultClientRoutingRules58,
			want: map[string]string{
				`(?i)^v2rayng/\d+\.\d+`:                               "v2ray",
				`^v2rayN/(?:6\.[4-9]\d*|[7-9]\.\d+|[1-9]\d{1,}\.\d+)`: "v2ray",
				`^Streisand`: "v2ray-json",
				`^Happ/(?:1\.63\.[1-9]|1\.6[4-9]\d*|1\.[7-9]\d*|[2-9]\.\d+)`: "happ",
			},
		},
		{
			name:   "admin pattern for v2rayNG is kept",
			stored: `[{"pattern":"(?i)^v2rayNG","result":"v2ray-json"}]`,
			want:   map[string]string{`(?i)^v2rayNG`: "v2ray-json"},
		},
		{
			name:   "admin already chose links",
			stored: `[{"pattern":"(?i)^v2rayng/\\d+\\.\\d+","result":"clash"}]`,
			want:   map[string]string{`(?i)^v2rayng/\d+\.\d+`: "clash"},
		},
		{name: "null", stored: nil},
		{name: "unreadable", stored: "not json"},
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
			if err := up000067V2rayNGPlainLinks(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			var got sql.NullString
			if err := db.QueryRow(`SELECT client_routing_rules FROM subscription_settings WHERE id = 1`).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if stored, _ := test.stored.(string); got.String != stored {
					t.Fatalf("rules = %q, want them untouched", got.String)
				}
				return
			}
			var rules []map[string]string
			if err := json.Unmarshal([]byte(got.String), &rules); err != nil {
				t.Fatalf("rules are no longer valid JSON: %v\n%s", err, got.String)
			}
			results := map[string]string{}
			for _, rule := range rules {
				results[rule["pattern"]] = rule["result"]
			}
			for pattern, want := range test.want {
				if results[pattern] != want {
					t.Fatalf("%s -> %q, want %q\n%s", pattern, results[pattern], want, got.String)
				}
			}
		})
	}
}
