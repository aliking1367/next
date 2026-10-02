package api

import (
	"context"
	"strings"
	"testing"
)

// The rule turns Cloudflare's checks off for the hostnames it names, so what it
// may name is the whole safety question. These are the guards, not conveniences.
func TestCDNSkipRuleHostnamesRefusesAnythingButManagedCDNNames(t *testing.T) {
	cases := []struct {
		name          string
		hostnames     []string
		suffix        string
		panelHostname string
		want          []string
	}{
		{
			name:          "ordinary node hostnames",
			hostnames:     []string{"amsterdam.example.com", "paris.example.com"},
			suffix:        "example.com",
			panelHostname: "panel.example.com",
			want:          []string{"amsterdam.example.com", "paris.example.com"},
		},
		{
			// The panel has a dashboard and an API behind it. A node named
			// "panel" would otherwise produce exactly this collision.
			name:          "the panel's own hostname is excluded",
			hostnames:     []string{"panel.example.com", "amsterdam.example.com"},
			suffix:        "example.com",
			panelHostname: "panel.example.com",
			want:          []string{"amsterdam.example.com"},
		},
		{
			name:          "the panel hostname is matched regardless of case",
			hostnames:     []string{"Panel.Example.COM"},
			suffix:        "example.com",
			panelHostname: "panel.example.com",
			want:          nil,
		},
		{
			// Nothing outside the zone this feature manages may widen the rule.
			name:          "another zone is excluded",
			hostnames:     []string{"node.someoneelse.com", "amsterdam.example.com"},
			suffix:        "example.com",
			panelHostname: "panel.example.com",
			want:          []string{"amsterdam.example.com"},
		},
		{
			name:          "the bare zone itself is excluded",
			hostnames:     []string{"example.com"},
			suffix:        "example.com",
			panelHostname: "",
			want:          nil,
		},
		{
			name:          "malformed values are dropped",
			hostnames:     []string{"", "   ", "no-dot", "has space.example.com", "a..example.com"},
			suffix:        "example.com",
			panelHostname: "",
			want:          nil,
		},
		{
			name:          "duplicates collapse",
			hostnames:     []string{"amsterdam.example.com", "AMSTERDAM.example.com"},
			suffix:        "example.com",
			panelHostname: "",
			want:          []string{"amsterdam.example.com"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := cdnSkipRuleHostnames(testCase.hostnames, testCase.suffix, testCase.panelHostname)
			if len(got) != len(testCase.want) {
				t.Fatalf("got %v, want %v", got, testCase.want)
			}
			for i := range got {
				if got[i] != testCase.want[i] {
					t.Fatalf("got %v, want %v", got, testCase.want)
				}
			}
		})
	}
}

// An empty list must produce no rule at all rather than a rule matching
// nothing, or a zone could end up carrying a skip rule for no reason.
func TestEnsureCDNSkipRuleDoesNothingWithoutHostnames(t *testing.T) {
	client := newCloudflareClient("unused-token")
	covered, err := client.ensureCDNSkipRule(context.Background(), "zone", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if covered != 0 {
		t.Fatalf("covered = %d, want 0", covered)
	}
}

func TestPanelOwnHostnameStripsSchemePortAndPath(t *testing.T) {
	cases := map[string]string{
		"https://panel.example.com":            "panel.example.com",
		"https://panel.example.com:2053":       "panel.example.com",
		"https://panel.example.com:2053/sub/":  "panel.example.com",
		"http://PANEL.example.com/":            "panel.example.com",
		"panel.example.com":                    "panel.example.com",
		"":                                     "",
	}
	for prefix, want := range cases {
		t.Run(prefix, func(t *testing.T) {
			server := newCDNAutomationTestServer(t)
			// Reading the settings once creates the single row the panel keeps
			// them in; without it the update below matches nothing.
			if _, err := server.settingsRepo.SubscriptionSettings(context.Background()); err != nil {
				t.Fatal(err)
			}
			if prefix != "" {
				result, err := server.db.Exec(
					`UPDATE subscription_settings SET subscription_url_prefix = ?`, prefix)
				if err != nil {
					t.Fatal(err)
				}
				if affected, err := result.RowsAffected(); err == nil && affected == 0 {
					t.Fatal("the prefix was not stored, so this test would pass for the wrong reason")
				}
			}
			if got := server.panelOwnHostname(context.Background()); got != want {
				t.Fatalf("panelOwnHostname() = %q, want %q", got, want)
			}
		})
	}
}

// The rule's expression is what Cloudflare matches on, so a hostname must reach
// it quoted and nothing else may slip in beside it.
func TestCDNSkipRuleDescriptionIsStable(t *testing.T) {
	// The description is how the panel finds its own rule again on the next
	// pass; changing it would orphan the previous one and leave two behind.
	if cdnSkipRuleDescription != "next-panel-cdn-skip" {
		t.Fatalf("description changed to %q, which would orphan rules already deployed", cdnSkipRuleDescription)
	}
	if !strings.Contains(customRulesPhase, "firewall_custom") {
		t.Fatalf("unexpected phase %q", customRulesPhase)
	}
}
