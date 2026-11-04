package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	telegramapp "github.com/aliking1367/next/internal/app/telegram"
)

const miniAppTestToken = "123456:TEST-token"

// signInitData builds initData the way Telegram does for a Mini App.
func signInitData(t *testing.T, token string, userID int64, authDate time.Time, extra map[string]string) string {
	t.Helper()
	values := url.Values{}
	values.Set("auth_date", fmt.Sprint(authDate.Unix()))
	values.Set("query_id", "AAHdF6IQAAAAAN0XohDhrOrc")
	values.Set("user", fmt.Sprintf(`{"id":%d,"first_name":"Test"}`, userID))
	for key, value := range extra {
		values.Set(key, value)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(token))
	values.Set("hash", hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(lines, "\n")))))
	return values.Encode()
}

func TestValidateTelegramInitData(t *testing.T) {
	now := time.Now()
	good := signInitData(t, miniAppTestToken, 4242, now, nil)
	if id, err := validateTelegramInitData(good, miniAppTestToken, now); err != nil || id != 4242 {
		t.Fatalf("good initData = %d %v", id, err)
	}
	// Newer clients add a signature field, which the hash also covers.
	withSignature := signInitData(t, miniAppTestToken, 4242, now, map[string]string{"signature": "abc"})
	if id, err := validateTelegramInitData(withSignature, miniAppTestToken, now); err != nil || id != 4242 {
		t.Fatalf("initData with signature = %d %v", id, err)
	}
	if _, err := validateTelegramInitData(good, "999:other-bot", now); err == nil {
		t.Fatal("initData signed for another bot must fail")
	}
	tampered := strings.Replace(good, "4242", "4243", 1)
	if _, err := validateTelegramInitData(tampered, miniAppTestToken, now); err == nil {
		t.Fatal("a changed user id must fail")
	}
	old := signInitData(t, miniAppTestToken, 4242, now.Add(-25*time.Hour), nil)
	if _, err := validateTelegramInitData(old, miniAppTestToken, now); err != errMiniAppExpired {
		t.Fatalf("day-old initData = %v, want expired", err)
	}
	for _, bad := range []string{"", "hash=", "auth_date=1&user=x"} {
		if _, err := validateTelegramInitData(bad, miniAppTestToken, now); err == nil {
			t.Fatalf("%q must fail", bad)
		}
	}
}

func TestMiniAppAccessAndActions(t *testing.T) {
	server, db := testBotMigratedServer(t)
	server.telegramRepo = telegramapp.NewRepository(db, "sqlite")
	if _, err := server.telegramRepo.Settings(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE telegram_settings SET api_token = '` + miniAppTestToken + `', use_telegram = 1, admin_chat_ids = '[1001]'`,
		`UPDATE admins SET telegram_id = 2002 WHERE username = 'seller'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	handler := server.Handler()
	call := func(telegramID int64, action string, body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/miniapp/"+action, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if telegramID != 0 {
			req.Header.Set(miniAppInitDataHeader, signInitData(t, miniAppTestToken, telegramID, time.Now(), nil))
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, _ := call(0, "me", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("unsigned call = %d", code)
	}
	if code, _ := call(3003, "me", `{}`); code != http.StatusForbidden {
		t.Fatalf("stranger = %d", code)
	}
	code, me := call(1001, "me", `{}`)
	if code != http.StatusOK || me["owner"] != true {
		t.Fatalf("owner me = %d %v", code, me)
	}
	code, me = call(2002, "me", `{}`)
	if code != http.StatusOK || me["owner"] != false || me["username"] != "seller" {
		t.Fatalf("seller me = %d %v", code, me)
	}

	if code, out := call(2002, "create", `{"username":"app_user","service_id":1,"gb":10,"days":30}`); code != http.StatusOK || out["username"] != "app_user" {
		t.Fatalf("seller create = %d %v", code, out)
	}
	code, out := call(2002, "renew", `{"username":"app_user","gb":5}`)
	if code != http.StatusOK || out["data_limit"].(float64) != float64(15<<30) {
		t.Fatalf("seller renew = %d %v", code, out)
	}
	if code, _ := call(2002, "status", `{"username":"app_user","status":"disabled"}`); code != http.StatusForbidden {
		t.Fatalf("seller status change = %d, want forbidden", code)
	}
	if code, out := call(1001, "status", `{"username":"app_user","status":"disabled"}`); code != http.StatusOK || out["status"] != "disabled" {
		t.Fatalf("owner status change = %d %v", code, out)
	}
	code, list := call(2002, "users", `{"kind":"a"}`)
	if code != http.StatusOK || len(list["users"].([]any)) != 1 {
		t.Fatalf("seller users = %d %v", code, list)
	}
	if code, out := call(1001, "create", `{"username":"owner_user","service_id":1}`); code != http.StatusOK {
		t.Fatalf("owner create = %d %v", code, out)
	}
	if code, _ := call(2002, "user", `{"username":"owner_user"}`); code != http.StatusNotFound {
		t.Fatalf("seller reading owner's user = %d, want not found", code)
	}
	if code, _ := call(2002, "delete", `{"username":"owner_user"}`); code == http.StatusOK {
		t.Fatal("seller must not delete the owner's user")
	}
	// A standard admin has no delete permission by default, and the Mini App
	// keeps to it like the dashboard does.
	if _, me := call(2002, "me", `{}`); me["can_delete"] != false {
		t.Fatalf("seller can_delete = %v", me["can_delete"])
	}
	if code, _ := call(2002, "delete", `{"username":"app_user"}`); code == http.StatusOK {
		t.Fatal("seller without the delete permission must not delete")
	}
	if code, _ := call(1001, "delete", `{"username":"app_user"}`); code != http.StatusOK {
		t.Fatalf("owner delete = %d", code)
	}

	// The page itself is public; the data behind it is not.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/miniapp", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "telegram-web-app.js") {
		t.Fatalf("page = %d", rec.Code)
	}
}

func TestMiniAppAddress(t *testing.T) {
	for input, want := range map[string]string{
		"https://next.example.com":            "https://next.example.com/miniapp",
		"https://next.example.com/":           "https://next.example.com/miniapp",
		"https://next.example.com/custom/app": "https://next.example.com/custom/app",
		"http://next.example.com":             "",
		"":                                    "",
	} {
		value := input
		if got := miniAppAddress(&value); got != want {
			t.Errorf("miniAppAddress(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMiniAppCreateWithAResetPeriod(t *testing.T) {
	server, db := testBotMigratedServer(t)
	server.telegramRepo = telegramapp.NewRepository(db, "sqlite")
	if _, err := server.telegramRepo.Settings(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE telegram_settings SET api_token = '` + miniAppTestToken + `', use_telegram = 1, admin_chat_ids = '[1001]'`,
		`UPDATE admins SET telegram_id = 2002 WHERE username = 'seller'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	handler := server.Handler()
	call := func(telegramID int64, action string, body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPost, "/api/miniapp/"+action, strings.NewReader(body))
		req.Header.Set(miniAppInitDataHeader, signInitData(t, miniAppTestToken, telegramID, time.Now(), nil))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if _, me := call(1001, "me", `{}`); me["can_reset"] != true {
		t.Fatalf("owner can_reset = %v", me["can_reset"])
	}
	if _, me := call(2002, "me", `{}`); me["can_reset"] != true {
		t.Fatalf("a standard admin may set a reset period by default, can_reset = %v", me["can_reset"])
	}
	// Take the permission away from the seller.
	perms := adminapp.RoleDefaultPermissions(adminapp.RoleStandard)
	perms.Users.PeriodicUsageReset = false
	raw, _ := json.Marshal(perms)
	if _, err := db.Exec(`UPDATE admins SET permissions = ? WHERE username = 'seller'`, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, me := call(2002, "me", `{}`); me["can_reset"] != false {
		t.Fatalf("seller without the permission can_reset = %v", me["can_reset"])
	}
	code, out := call(1001, "create", `{"username":"monthly","service_id":1,"gb":50,"days":30,"reset_strategy":"month"}`)
	if code != http.StatusOK || out["reset_strategy"] != "month" {
		t.Fatalf("owner create = %d %v", code, out)
	}
	if code, _ := call(1001, "create", `{"username":"bad_period","service_id":1,"gb":50,"reset_strategy":"hourly"}`); code != http.StatusBadRequest {
		t.Fatalf("unknown period = %d", code)
	}
	if code, _ := call(2002, "create", `{"username":"seller_monthly","service_id":1,"gb":50,"reset_strategy":"month"}`); code == http.StatusOK {
		t.Fatal("an admin without the periodic reset permission must not set one")
	}
	var stored string
	if err := db.QueryRow(`SELECT COALESCE(data_limit_reset_strategy, '') FROM users WHERE username = 'monthly'`).Scan(&stored); err != nil || stored != "month" {
		t.Fatalf("stored = %q %v", stored, err)
	}
}
