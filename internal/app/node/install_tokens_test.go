package node

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func installTokenRepo(t *testing.T) (Repository, *sql.DB, time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "tokens.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE nodes (id INTEGER PRIMARY KEY, name TEXT, address TEXT, port INTEGER, api_port INTEGER,
			status TEXT, certificate TEXT, certificate_key TEXT, usage_coefficient REAL DEFAULT 1,
			xray_config_mode TEXT, uplink INTEGER DEFAULT 0, downlink INTEGER DEFAULT 0)`,
		`CREATE TABLE node_install_tokens (id INTEGER PRIMARY KEY AUTOINCREMENT, node_id INTEGER NOT NULL,
			token_hash TEXT NOT NULL UNIQUE, created_at DATETIME NOT NULL, expires_at DATETIME NOT NULL, used_at DATETIME NULL)`,
		`INSERT INTO nodes (id, name, address, port, api_port, status, certificate, certificate_key, xray_config_mode)
			VALUES (1, 'Turkey', 'turk.example.com', 62050, 62052, 'connecting',
			'-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----',
			'-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----', 'default')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	repo := Repository{db: db, now: func() time.Time { return now }}
	return repo, db, now
}

// The token is the whole credential for a node's private key, so it must work
// exactly once.
func TestInstallTokenIsSingleUse(t *testing.T) {
	repo, _, _ := installTokenRepo(t)
	ctx := context.Background()

	token, err := repo.CreateInstallToken(ctx, 1, time.Hour)
	if err != nil {
		t.Fatalf("CreateInstallToken: %v", err)
	}
	if len(token.Token) < 32 {
		t.Fatalf("token looks too short to be unguessable: %q", token.Token)
	}

	bundle, err := repo.RedeemInstallToken(ctx, token.Token)
	if err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if !strings.Contains(bundle.InstallBundlePEM(), "-----END CERTIFICATE-----") ||
		!strings.Contains(bundle.InstallBundlePEM(), "-----END PRIVATE KEY-----") {
		t.Fatalf("bundle is missing a half: %q", bundle.InstallBundlePEM())
	}

	if _, err := repo.RedeemInstallToken(ctx, token.Token); err != ErrInstallTokenInvalid {
		t.Fatalf("second redeem = %v, want it refused", err)
	}
}

// A token left in a chat log or a provider's startup-script field has to stop
// working on its own.
func TestInstallTokenExpires(t *testing.T) {
	repo, db, now := installTokenRepo(t)
	ctx := context.Background()

	token, err := repo.CreateInstallToken(ctx, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	later := Repository{db: db, now: func() time.Time { return now.Add(2 * time.Hour) }}
	if _, err := later.RedeemInstallToken(ctx, token.Token); err != ErrInstallTokenInvalid {
		t.Fatalf("expired token = %v, want it refused", err)
	}
}

func TestInstallTokenRejectsUnknownAndBlank(t *testing.T) {
	repo, _, _ := installTokenRepo(t)
	ctx := context.Background()
	for name, token := range map[string]string{
		"blank":   "   ",
		"unknown": "0123456789abcdef0123456789abcdef",
	} {
		if _, err := repo.RedeemInstallToken(ctx, token); err != ErrInstallTokenInvalid {
			t.Errorf("%s token = %v, want it refused", name, err)
		}
	}
}

// Issuing a new token has to retire the old one, or a link the admin believes
// they replaced still installs a node.
func TestInstallTokenReplacesThePreviousOne(t *testing.T) {
	repo, _, _ := installTokenRepo(t)
	ctx := context.Background()

	first, err := repo.CreateInstallToken(ctx, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateInstallToken(ctx, 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token {
		t.Fatal("a new token must not repeat the old one")
	}
	if _, err := repo.RedeemInstallToken(ctx, first.Token); err != ErrInstallTokenInvalid {
		t.Errorf("the replaced token = %v, want it refused", err)
	}
	if _, err := repo.RedeemInstallToken(ctx, second.Token); err != nil {
		t.Errorf("the current token should still work: %v", err)
	}
}

// The raw token must not be recoverable from the database.
func TestInstallTokenIsStoredHashed(t *testing.T) {
	repo, db, _ := installTokenRepo(t)
	token, err := repo.CreateInstallToken(context.Background(), 1, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT token_hash FROM node_install_tokens WHERE node_id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token.Token {
		t.Fatal("the token is stored in the clear")
	}
	if len(stored) != 64 {
		t.Fatalf("token_hash = %q, want a sha256 hex digest", stored)
	}
}
