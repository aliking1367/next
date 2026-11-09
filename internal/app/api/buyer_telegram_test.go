package api

import (
	"context"
	"database/sql"
	"testing"
)

// The buyer's Telegram ID moves from the sales bot's note into the empty
// Telegram ID field; one an admin typed in is never replaced.
func TestBuyerTelegramIDsFillOnlyEmptyFields(t *testing.T) {
	server, _ := testBotMigratedServer(t)
	ctx := context.Background()
	for _, row := range []struct {
		id         int
		note       string
		telegramID any
	}{
		{1, "New account created by 5683497583 at 09-11-2026 17:22:58", nil},
		{2, "New account created by 111222333 at 09-11-2026", "999888777"},
		{3, "regular customer", nil},
	} {
		if _, err := server.db.ExecContext(ctx, `INSERT INTO users (id, username, status, note, telegram_id, credential_key) VALUES (?, ?, 'active', ?, ?, ?)`,
			row.id, "buyer"+string(rune('0'+row.id)), row.note, row.telegramID, "0123456789abcdef0123456789abcde"+string(rune('0'+row.id))); err != nil {
			t.Fatal(err)
		}
	}
	filled, err := server.fillBuyerTelegramIDs(ctx)
	if err != nil || filled != 1 {
		t.Fatalf("filled = %d, %v; want 1", filled, err)
	}
	want := map[int]string{1: "5683497583", 2: "999888777", 3: ""}
	for id, expected := range want {
		var got sql.NullString
		if err := server.db.QueryRowContext(ctx, `SELECT telegram_id FROM users WHERE id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got.String != expected {
			t.Errorf("user %d telegram_id = %q, want %q", id, got.String, expected)
		}
	}
	// A second pass has nothing left to do.
	if filled, err := server.fillBuyerTelegramIDs(ctx); err != nil || filled != 0 {
		t.Fatalf("second pass filled %d, %v", filled, err)
	}
}
