package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

func TestSubscriptionPageTemplateGalleryListsAndPreviews(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root) // the server finds bundled templates next to its working directory

	server, db := testAdminServer(t)
	insertMasterAPIAdmin(t, db, 1, "root", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	token := adminBearerToken(t, server, "root", "pass123")

	rec := adminJSONRequest(t, server, http.MethodGet, "/api/settings/subscriptions/page-templates", token, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"subscription/index.html": false, "subscription/midnight.html": false, "subscription/pearl.html": false, "subscription/ember.html": false}
	for _, name := range list.Templates {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("template %s missing from %v", name, list.Templates)
		}
	}

	for _, name := range list.Templates {
		rec = adminJSONRequest(t, server, http.MethodGet, "/api/settings/subscriptions/page-templates/preview?name="+name, token, ``)
		if rec.Code != http.StatusOK {
			t.Fatalf("preview %s status = %d body=%s", name, rec.Code, rec.Body.String())
		}
		var preview struct {
			HTML string `json:"html"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(preview.HTML, "demo_user") {
			t.Fatalf("preview %s does not show the demo user", name)
		}
	}

	// Only bundled subscription pages can be previewed.
	for _, name := range []string{"../../go.mod", "clash/default.yml", "subscription/../../go.mod", "subscription/missing.html"} {
		rec = adminJSONRequest(t, server, http.MethodGet, "/api/settings/subscriptions/page-templates/preview?name="+name, token, ``)
		if rec.Code == http.StatusOK {
			t.Fatalf("preview of %q must be refused", name)
		}
	}
	// And only by admins who may change subscription settings.
	rec = adminJSONRequest(t, server, http.MethodGet, "/api/settings/subscriptions/page-templates", "", ``)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous list status = %d", rec.Code)
	}
}
