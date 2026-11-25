package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	telegramapp "github.com/aliking1367/next/internal/app/telegram"
)

// Telegram rejects messages longer than 4096 characters.
const adminInvoiceMaxChars = 4000

type adminInvoiceSendPayload struct {
	Text string `json:"text"`
}

// handleAdminInvoiceTelegram sends an invoice the dashboard prepared to the
// admin's own Telegram account, through the panel's bot. The text goes out
// as plain text, exactly as the owner saw and edited it.
func (s *Server) handleAdminInvoiceTelegram(w http.ResponseWriter, r *http.Request, username string) {
	principal, _ := r.Context().Value(adminContextKey).(adminPrincipal)
	var payload adminInvoiceSendPayload
	if err := decodeOptionalJSON(r, &payload); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "Invoice text is empty")
		return
	}
	if utf8.RuneCountInString(text) > adminInvoiceMaxChars {
		writeError(w, http.StatusBadRequest, "Invoice text is too long")
		return
	}
	var chatID int64
	err := s.withTx(r.Context(), func(tx *sql.Tx) error {
		target, err := adminByUsernameTx(r.Context(), tx, username)
		if err != nil {
			return err
		}
		// The admin's own Telegram is not an invoice recipient.
		if strings.EqualFold(principal.Context.Admin.Username, target.Username) {
			return statusError{status: http.StatusBadRequest, detail: "Cannot send an invoice to yourself"}
		}
		if err := ensureCanManageAdmin(principal.Context.Admin, target); err != nil {
			return err
		}
		if target.TelegramID == nil || *target.TelegramID == 0 {
			return statusError{status: http.StatusConflict, detail: "admin_has_no_telegram_id"}
		}
		chatID = *target.TelegramID
		return nil
	})
	if err != nil {
		writeStatusError(w, err)
		return
	}
	_, err = s.telegramSender.SendMessage(r.Context(), telegramapp.MessageRequest{
		Destination:           telegramapp.DestinationRequest{ChatID: &chatID},
		Text:                  text,
		DisableWebPagePreview: true,
	})
	if err != nil {
		if errors.Is(err, telegramapp.ErrNotConfigured) {
			writeError(w, http.StatusConflict, "telegram_not_configured")
			return
		}
		// Most often the admin has not started the panel's bot yet, so
		// Telegram does not let the bot message them.
		writeError(w, http.StatusBadGateway, "telegram_send_failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
