package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	telegramapp "github.com/aliking1367/next/internal/app/telegram"
)

func TestAdminInvoiceTelegramSendsToTheAdmin(t *testing.T) {
	server, db := testAdminServer(t)
	createSettingsTables(t, db)
	insertMasterAPIAdmin(t, db, 1, "owner", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 2, "seller", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 3, "nochat", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	if _, err := db.Exec(`UPDATE admins SET telegram_id = 555 WHERE username = 'seller'`); err != nil {
		t.Fatal(err)
	}

	var sent map[string]any
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Fatalf("unexpected Telegram API path: %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &sent); err != nil {
			t.Fatalf("decode Telegram payload: %v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()
	server.telegramRepo = telegramapp.NewRepository(db, "sqlite")
	server.telegramSender = telegramapp.NewSender(server.telegramRepo, api.URL)
	if _, err := db.Exec(`INSERT INTO telegram_settings (id, api_token, use_telegram, admin_chat_ids) VALUES (1, 'token', 1, '[111]')`); err != nil {
		t.Fatal(err)
	}

	ownerToken := adminBearerToken(t, server, "owner", "pass123")
	rec := adminJSONRequest(t, server, http.MethodPost, "/api/admin/seller/invoice/telegram", ownerToken, `{"text":"🧾 invoice <b>1</b>"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("send status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sent["chat_id"] != float64(555) || sent["text"] != "🧾 invoice <b>1</b>" {
		t.Fatalf("unexpected Telegram payload: %#v", sent)
	}
	if _, ok := sent["parse_mode"]; ok {
		t.Fatalf("invoice must go out as plain text: %#v", sent)
	}

	rec = adminJSONRequest(t, server, http.MethodPost, "/api/admin/nochat/invoice/telegram", ownerToken, `{"text":"x"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "admin_has_no_telegram_id") {
		t.Fatalf("admin without telegram id: status=%d body=%s", rec.Code, rec.Body.String())
	}

	rec = adminJSONRequest(t, server, http.MethodPost, "/api/admin/seller/invoice/telegram", ownerToken, `{"text":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty text: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// A standard admin cannot send invoices to other admins.
	sellerToken := adminBearerToken(t, server, "nochat", "pass123")
	rec = adminJSONRequest(t, server, http.MethodPost, "/api/admin/seller/invoice/telegram", sellerToken, `{"text":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("standard admin: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
