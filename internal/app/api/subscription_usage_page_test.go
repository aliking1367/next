package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubscriptionUsageRespondsWithPageOnlyToBrowsers(t *testing.T) {
	for _, test := range []struct {
		accept, query string
		want          bool
	}{
		{"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8", "", true},
		{"*/*", "", false},
		{"application/json", "", false},
		{"text/html", "?format=json", false},
	} {
		r := httptest.NewRequest(http.MethodGet, "/sub/abc/usage"+test.query, nil)
		r.Header.Set("Accept", test.accept)
		if got := wantsSubscriptionUsagePage(r); got != test.want {
			t.Fatalf("accept=%q query=%q: page=%v, want %v", test.accept, test.query, got, test.want)
		}
	}
}

func TestSubscriptionUsagePageEmbedsDataSafely(t *testing.T) {
	payload := map[string]any{
		"username":      "</script><script>alert(1)</script>",
		"start":         "2026-08-23T00:00:00Z",
		"end":           "2026-09-22T00:00:00Z",
		"usages":        []map[string]any{{"date": "2026-09-21", "used_traffic": 5 << 30}, {"date": "2026-09-22", "used_traffic": 130134395}},
		"node_usages":   []map[string]any{{"node_id": 1, "node_name": "king", "uplink": 0, "downlink": 130134395}},
		"hourly_usages": []any{},
	}
	rec := httptest.NewRecorder()
	writeSubscriptionUsagePage(rec, payload)
	body := rec.Body.String()
	if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", rec.Header().Get("Content-Type"))
	}
	if strings.Contains(body, "</script><script>alert(1)") || strings.Contains(body, "__USAGE_JSON__") {
		t.Fatal("payload must be embedded escaped")
	}
	if !strings.Contains(body, `\u003c/script\u003e`) {
		t.Fatal("expected the username to be JSON-escaped in the page")
	}
	if dir := os.Getenv("NEXT_TEMPLATE_PREVIEW_DIR"); dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "usage.html"), []byte(body), 0o644)
	}
}
