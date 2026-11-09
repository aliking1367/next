package api

import (
	"context"
	"database/sql"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	userapp "github.com/aliking1367/next/internal/app/user"
)

// The sales bot writes the buyer's Telegram ID only into the note of the
// account it creates. A background pass copies it into the user's Telegram ID
// field, for every account whose field is still empty, so each customer's
// accounts can be found and shown by their Telegram ID. It runs apart from
// the API, which the bot relies on and which stays exactly as it was; it only
// fills an empty field and never overwrites one an admin set.

const buyerTelegramFillEvery = 2 * time.Minute

func (s *Server) runBuyerTelegramFill(ctx context.Context) {
	// A first pass soon after start fills the accounts made before this.
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if filled, err := s.fillBuyerTelegramIDs(ctx); err != nil {
				logging.Warnf(logging.ComponentTelegram, "buyer Telegram IDs: %v", err)
			} else if filled > 0 {
				logging.Infof(logging.ComponentTelegram, "buyer Telegram IDs: filled %d accounts from their notes", filled)
			}
			timer.Reset(buyerTelegramFillEvery)
		}
	}
}

func (s *Server) fillBuyerTelegramIDs(ctx context.Context) (int, error) {
	passCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	rows, err := s.db.QueryContext(passCtx, `SELECT id, note FROM users
WHERE (telegram_id IS NULL OR TRIM(telegram_id) = '') AND LOWER(COALESCE(note, '')) LIKE '%created by%'`)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id    int64
		buyer string
	}
	var found []pending
	for rows.Next() {
		var id int64
		var note sql.NullString
		if err := rows.Scan(&id, &note); err != nil {
			rows.Close()
			return 0, err
		}
		if buyer := userapp.BuyerTelegramID(note.String); buyer != "" {
			found = append(found, pending{id: id, buyer: buyer})
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	filled := 0
	for _, item := range found {
		// The empty-field check is repeated in the update, so an ID an admin
		// typed in meanwhile is never replaced.
		result, err := s.db.ExecContext(passCtx, `UPDATE users SET telegram_id = ?
WHERE id = ? AND (telegram_id IS NULL OR TRIM(telegram_id) = '')`, item.buyer, item.id)
		if err != nil {
			return filled, err
		}
		if n, _ := result.RowsAffected(); n > 0 {
			filled++
		}
	}
	return filled, nil
}
