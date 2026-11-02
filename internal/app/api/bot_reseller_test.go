package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	"github.com/aliking1367/next/internal/app/migrations"
	"github.com/aliking1367/next/internal/app/nodecontroller"
	telegrambot "github.com/aliking1367/next/internal/app/telegram/bot"
	userapp "github.com/aliking1367/next/internal/app/user"
)

func TestRenewFieldsAddOnTopOfWhatIsLeft(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	future := now.Unix() + 5*86400
	past := now.Unix() - 3*86400
	limit := int64(10 << 30)
	hold := int64(7 * 86400)

	cases := []struct {
		name   string
		detail userapp.UserDetail
		days   int64
		gb     int64
		want   map[string]int64
	}{
		{"days on a running expiry", userapp.UserDetail{Status: "active", Expire: &future}, 30, 0, map[string]int64{"expire": future + 30*86400}},
		{"days on a passed expiry start today", userapp.UserDetail{Status: "expired", Expire: &past}, 30, 0, map[string]int64{"expire": now.Unix() + 30*86400}},
		{"gigabytes on the limit", userapp.UserDetail{Status: "limited", DataLimit: &limit}, 0, 20, map[string]int64{"data_limit": limit + 20<<30}},
		{"on hold gets a longer wait", userapp.UserDetail{Status: "on_hold", OnHoldExpireDuration: &hold}, 10, 0, map[string]int64{"on_hold_expire_duration": hold + 10*86400}},
		{"unlimited stays unlimited", userapp.UserDetail{Status: "active"}, 30, 20, map[string]int64{}},
	}
	for _, tc := range cases {
		got := renewFields(tc.detail, tc.days, tc.gb, now)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: fields = %v, want %v", tc.name, got, tc.want)
		}
		for key, value := range tc.want {
			if got[key] != value {
				t.Fatalf("%s: %s = %v, want %d", tc.name, key, got[key], value)
			}
		}
	}
}

func TestBotFindsAdminByTelegramID(t *testing.T) {
	server, db := testAdminServer(t)
	insertMasterAPIAdmin(t, db, 1, "owner", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 2, "seller", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 3, "off", "pass123", adminapp.RoleStandard, adminapp.StatusDisabled)
	insertMasterAPIAdmin(t, db, 4, "twin1", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 5, "twin2", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	for _, statement := range []string{
		`UPDATE admins SET telegram_id = 1001 WHERE id = 2`,
		`UPDATE admins SET telegram_id = 1002 WHERE id = 3`,
		`UPDATE admins SET telegram_id = 1003 WHERE id IN (4, 5)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	auth := botAuthorizer{server: server}
	ctx := context.Background()

	actor, ok := auth.AdminByTelegramID(ctx, 1001)
	if !ok || actor.Username != "seller" {
		t.Fatalf("1001 = %+v %v, want seller", actor, ok)
	}
	if _, ok := actor.Admin.(adminapp.Admin); !ok {
		t.Fatalf("actor admin = %T", actor.Admin)
	}
	for _, id := range []int64{1002, 1003, 9999, 0} {
		if actor, ok := auth.AdminByTelegramID(ctx, id); ok {
			t.Fatalf("%d should not match, got %+v", id, actor)
		}
	}
}

func TestBotResellerCreatesRenewsAndOnlySeesItsOwnUsers(t *testing.T) {
	server, _ := testBotMigratedServer(t)
	seller, ok, err := server.adminRepo.AdminByUsername(context.Background(), "seller")
	if err != nil || !ok {
		t.Fatalf("seller: %v %v", ok, err)
	}
	owner, _, _ := server.adminRepo.AdminByUsername(context.Background(), "owner")
	users := botUserService{server: server}
	ctx := context.Background()
	sellerActor := telegrambot.Actor{Username: "seller", Admin: seller, Reseller: true}
	ownerActor := telegrambot.Actor{Username: "owner", Admin: owner}

	services, err := users.Services(ctx, sellerActor)
	if err != nil || len(services) != 1 || services[0].ID != 1 {
		t.Fatalf("services = %+v %v", services, err)
	}
	if err := users.Create(ctx, sellerActor, telegrambot.NewUser{Username: "botuser", ServiceID: 1, DataLimitGB: 10, Days: 30}); err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := users.Get(ctx, sellerActor, "botuser")
	if err != nil || view.OwnerAdmin != "seller" || view.DataLimit == nil || *view.DataLimit != 10<<30 || view.Expire == nil {
		t.Fatalf("created view = %+v %v", view, err)
	}
	firstExpire := *view.Expire

	if err := users.Renew(ctx, sellerActor, "botuser", 30, 5); err != nil {
		t.Fatalf("renew: %v", err)
	}
	view, _ = users.Get(ctx, sellerActor, "botuser")
	if *view.DataLimit != 15<<30 || *view.Expire != firstExpire+30*86400 {
		t.Fatalf("after renew limit=%d expire=%d (was %d)", *view.DataLimit, *view.Expire, firstExpire)
	}

	// The owner's own user is invisible and untouchable for the seller.
	if err := users.Create(ctx, ownerActor, telegrambot.NewUser{Username: "ownersuser", ServiceID: 1}); err != nil {
		t.Fatalf("owner create: %v", err)
	}
	if _, err := users.Get(ctx, sellerActor, "ownersuser"); err == nil {
		t.Fatal("seller must not see the owner's user")
	}
	if err := users.Delete(ctx, sellerActor, "ownersuser"); err == nil {
		t.Fatal("seller must not delete the owner's user")
	}
	if _, err := users.Get(ctx, ownerActor, "ownersuser"); err != nil {
		t.Fatalf("owner user should still exist: %v", err)
	}
}

// testBotMigratedServer is a server on a fully migrated database, so user
// creation sees every column it writes. "owner" (full access) and "seller"
// (standard, on service 1) exist.
func testBotMigratedServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "bot.db")+"?_pragma=busy_timeout(30000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrations.RunMigrations(context.Background(), db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	for id, admin := range map[int64]adminapp.AdminRole{1: adminapp.RoleFullAccess, 2: adminapp.RoleStandard} {
		username := map[int64]string{1: "owner", 2: "seller"}[id]
		permissions, err := json.Marshal(adminapp.RoleDefaultPermissions(admin))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO admins (
			id, username, hashed_password, role, permissions, status, subscription_settings,
			users_usage, lifetime_usage, created_traffic, deleted_users_usage,
			traffic_limit_mode, use_service_traffic_limits, show_user_traffic, delete_user_usage_limit_enabled
		) VALUES (?, ?, 'unused', ?, ?, 'active', '{}', 0, 0, 0, 0, 'used_traffic', 0, 1, 0)`,
			id, username, string(admin), string(permissions)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT OR REPLACE INTO xray_config (id, data) VALUES (1, '{"inbounds":[{"tag":"vless-in","protocol":"vless","port":443,"settings":{"decryption":"none"},"streamSettings":{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}}]}')`,
		`INSERT OR IGNORE INTO inbounds (id, tag) VALUES (1, 'vless-in')`,
		`INSERT INTO hosts (id, inbound_tag, remark, address, is_disabled) VALUES (1, 'vless-in', 'main', 'example.com', 0)`,
		`INSERT INTO services (id, name) VALUES (1, 'main')`,
		`INSERT INTO service_hosts (service_id, host_id, sort) VALUES (1, 1, 0)`,
		`INSERT INTO admins_services (admin_id, service_id) VALUES (2, 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	server := &Server{
		db:             db,
		dialect:        "sqlite",
		adminRepo:      adminapp.NewRepository(db, "sqlite"),
		userService:    userapp.NewService(userapp.NewRepository(db, "sqlite")),
		nodeController: nodecontroller.NewController(nodecontroller.NewRepository(db, "sqlite")),
	}
	return server, db
}

func TestBotListsAreScopedAndFiltered(t *testing.T) {
	server, db := testBotMigratedServer(t)
	ctx := context.Background()
	seller, _, _ := server.adminRepo.AdminByUsername(ctx, "seller")
	owner, _, _ := server.adminRepo.AdminByUsername(ctx, "owner")
	users := botUserService{server: server}
	sellerActor := telegrambot.Actor{Username: "seller", Admin: seller, Reseller: true}
	ownerActor := telegrambot.Actor{Username: "owner", Admin: owner}

	for _, item := range []struct {
		actor telegrambot.Actor
		name  string
		days  int64
	}{
		{sellerActor, "s_soon", 2},
		{sellerActor, "s_later", 40},
		{sellerActor, "s_done", 40},
		{ownerActor, "o_soon", 1},
	} {
		if err := users.Create(ctx, item.actor, telegrambot.NewUser{Username: item.name, ServiceID: 1, DataLimitGB: 10, Days: item.days}); err != nil {
			t.Fatalf("create %s: %v", item.name, err)
		}
	}
	if _, err := db.Exec(`UPDATE users SET status = 'limited', used_traffic = data_limit WHERE username = 's_done'`); err != nil {
		t.Fatal(err)
	}

	names := func(actor telegrambot.Actor, query telegrambot.UserListQuery) []string {
		query.Limit = 10
		views, total, err := users.List(ctx, actor, query)
		if err != nil {
			t.Fatalf("list %+v: %v", query, err)
		}
		out := []string{}
		for _, view := range views {
			out = append(out, view.Username)
		}
		if int64(len(out)) != total {
			t.Fatalf("list %+v total=%d rows=%v", query, total, out)
		}
		return out
	}
	has := func(list []string, name string) bool {
		for _, item := range list {
			if item == name {
				return true
			}
		}
		return false
	}

	all := names(sellerActor, telegrambot.UserListQuery{Kind: telegrambot.ListAll})
	if len(all) != 3 || has(all, "o_soon") {
		t.Fatalf("seller all = %v", all)
	}
	if got := names(sellerActor, telegrambot.UserListQuery{Kind: telegrambot.ListExpiring}); len(got) != 1 || got[0] != "s_soon" {
		t.Fatalf("seller expiring = %v", got)
	}
	if got := names(sellerActor, telegrambot.UserListQuery{Kind: telegrambot.ListFinished}); len(got) != 1 || got[0] != "s_done" {
		t.Fatalf("seller finished = %v", got)
	}
	if got := names(sellerActor, telegrambot.UserListQuery{Kind: telegrambot.ListSearch, Search: "soon"}); len(got) != 1 || got[0] != "s_soon" {
		t.Fatalf("seller search = %v", got)
	}
	if got := names(ownerActor, telegrambot.UserListQuery{Kind: telegrambot.ListExpiring}); !has(got, "o_soon") || !has(got, "s_soon") {
		t.Fatalf("owner expiring = %v", got)
	}
}
