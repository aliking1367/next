package api

import (
	"net/http"
	"testing"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

func TestDeleteAdminCanMoveItsUsersToAnotherAdmin(t *testing.T) {
	server, db := testAdminServer(t)
	insertMasterAPIAdmin(t, db, 1, "owner", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 3, "reseller", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 4, "keeper", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	for _, statement := range []string{
		`INSERT INTO services (id, name) VALUES (5, 'main')`,
		`INSERT INTO admins_services (admin_id, service_id) VALUES (3, 5), (4, 5)`,
		`INSERT INTO users (id, username, admin_id, service_id, status) VALUES (10, 'u10', 3, 5, 'active')`,
		// Switched off only because the reseller hit its own limit.
		`INSERT INTO users (id, username, admin_id, service_id, status, admin_disabled_at) VALUES (11, 'u11', 3, 5, 'disabled', '2026-11-01 10:00:00')`,
		// Switched off by hand; it stays off.
		`INSERT INTO users (id, username, admin_id, service_id, status) VALUES (12, 'u12', 3, 5, 'disabled')`,
		`INSERT INTO users (id, username, admin_id, status) VALUES (13, 'u13', 3, 'deleted')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	token := adminBearerToken(t, server, "owner", "pass123")

	rec := adminJSONRequest(t, server, http.MethodDelete, "/api/admin/reseller?transfer_users_to=keeper", token, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete with transfer status = %d body=%s", rec.Code, rec.Body.String())
	}

	want := map[int64][2]any{
		10: {int64(4), "active"},
		11: {int64(4), "active"},
		12: {int64(4), "disabled"},
		13: {int64(3), "deleted"},
	}
	for id, expected := range want {
		var adminID int64
		var status string
		if err := db.QueryRow(`SELECT admin_id, status FROM users WHERE id = ?`, id).Scan(&adminID, &status); err != nil {
			t.Fatal(err)
		}
		if adminID != expected[0] || status != expected[1] {
			t.Fatalf("user %d admin=%d status=%q, want admin=%v status=%v", id, adminID, status, expected[0], expected[1])
		}
	}
	var removed, enabled int
	if err := db.QueryRow(`SELECT COUNT(*) FROM node_operations WHERE operation_type = 'remove_user'`).Scan(&removed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM node_operations WHERE operation_type = 'enable_user' AND user_id = 11`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if removed != 0 || enabled != 1 {
		t.Fatalf("node operations remove_user=%d enable_user(u11)=%d", removed, enabled)
	}
	var resellerStatus string
	if err := db.QueryRow(`SELECT status FROM admins WHERE id = 3`).Scan(&resellerStatus); err != nil {
		t.Fatal(err)
	}
	if resellerStatus != "deleted" {
		t.Fatalf("reseller status = %q", resellerStatus)
	}
}

func TestDeleteAdminTransferRefusesAnAdminWithoutTheService(t *testing.T) {
	server, db := testAdminServer(t)
	insertMasterAPIAdmin(t, db, 1, "owner", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 3, "reseller", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 4, "keeper", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	for _, statement := range []string{
		`INSERT INTO services (id, name) VALUES (5, 'main')`,
		`INSERT INTO admins_services (admin_id, service_id) VALUES (3, 5)`,
		`INSERT INTO users (id, username, admin_id, service_id, status) VALUES (10, 'u10', 3, 5, 'active')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	token := adminBearerToken(t, server, "owner", "pass123")

	rec := adminJSONRequest(t, server, http.MethodDelete, "/api/admin/reseller?transfer_users_to=keeper", token, `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete with transfer status = %d body=%s", rec.Code, rec.Body.String())
	}
	// Nothing happened: the admin and its user are untouched.
	var adminStatus, userStatus string
	var adminID int64
	if err := db.QueryRow(`SELECT status FROM admins WHERE id = 3`).Scan(&adminStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT admin_id, status FROM users WHERE id = 10`).Scan(&adminID, &userStatus); err != nil {
		t.Fatal(err)
	}
	if adminStatus != "active" || adminID != 3 || userStatus != "active" {
		t.Fatalf("after refusal admin=%q user admin=%d status=%q", adminStatus, adminID, userStatus)
	}
}

func TestDeleteAdminTransferRespectsTheReceiverUsersLimit(t *testing.T) {
	server, db := testAdminServer(t)
	insertMasterAPIAdmin(t, db, 1, "owner", "pass123", adminapp.RoleFullAccess, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 3, "reseller", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	insertMasterAPIAdmin(t, db, 4, "keeper", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	for _, statement := range []string{
		`UPDATE admins SET users_limit = 1 WHERE id = 4`,
		`INSERT INTO users (id, username, admin_id, status) VALUES (10, 'u10', 3, 'active'), (11, 'u11', 3, 'active')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	token := adminBearerToken(t, server, "owner", "pass123")

	rec := adminJSONRequest(t, server, http.MethodDelete, "/api/admin/reseller?transfer_users_to=keeper", token, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete with transfer status = %d body=%s", rec.Code, rec.Body.String())
	}
	var moved int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE admin_id = 4`).Scan(&moved); err != nil {
		t.Fatal(err)
	}
	if moved != 0 {
		t.Fatalf("moved %d users past the limit", moved)
	}
}

func TestBulkChangeAdminMovesSelectedUsers(t *testing.T) {
	server, db, ownerToken := testUserMutationServer(t)
	insertMasterAPIAdmin(t, db, 4, "keeper", "pass123", adminapp.RoleStandard, adminapp.StatusActive)
	for _, statement := range []string{
		`INSERT INTO admins_services (admin_id, service_id) VALUES (2, 1), (4, 1)`,
		`INSERT INTO users (id, username, admin_id, status, credential_key, created_at, service_id) VALUES
			(10, 'u10', 2, 'active', '00000000000000000000000000000010', '2026-01-01 00:00:00', 1),
			(11, 'u11', 2, 'active', '00000000000000000000000000000011', '2026-01-01 00:00:00', 1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}

	rec := adminJSONRequest(t, server, http.MethodPost, "/api/users/actions", ownerToken, `{"action":"change_admin","admin_username":"seller","target_admin_username":"keeper","usernames":["u10"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("change_admin status = %d body=%s", rec.Code, rec.Body.String())
	}
	var u10, u11 int64
	if err := db.QueryRow(`SELECT admin_id FROM users WHERE id = 10`).Scan(&u10); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT admin_id FROM users WHERE id = 11`).Scan(&u11); err != nil {
		t.Fatal(err)
	}
	if u10 != 4 || u11 != 2 {
		t.Fatalf("u10 admin=%d u11 admin=%d, want only u10 moved", u10, u11)
	}

	// Without a source admin there is nothing to move from.
	rec = adminJSONRequest(t, server, http.MethodPost, "/api/users/actions", ownerToken, `{"action":"change_admin","target_admin_username":"keeper"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("change_admin without source status = %d body=%s", rec.Code, rec.Body.String())
	}

	// A standard admin cannot hand its users to someone else.
	sellerToken := adminBearerToken(t, server, "seller", "pass123")
	rec = adminJSONRequest(t, server, http.MethodPost, "/api/users/actions", sellerToken, `{"action":"change_admin","target_admin_username":"keeper"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("standard change_admin status = %d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.QueryRow(`SELECT admin_id FROM users WHERE id = 11`).Scan(&u11); err != nil {
		t.Fatal(err)
	}
	if u11 != 2 {
		t.Fatalf("standard admin moved u11 to %d", u11)
	}
}
