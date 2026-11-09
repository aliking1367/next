package user

import (
	"regexp"
	"strings"
)

// The sales bot cannot be changed, and it records who bought an account only
// in the note it writes: "New account created by 5683497583 at ...". The
// number is the buyer's Telegram ID, so the panel reads it from there.
var buyerTelegramIDPattern = regexp.MustCompile(`(?i)\bcreated by\s+(\d{5,16})\b`)

// BuyerTelegramID is the buyer's Telegram ID in a sales bot's note, or "".
func BuyerTelegramID(note string) string {
	match := buyerTelegramIDPattern.FindStringSubmatch(strings.TrimSpace(note))
	if match == nil {
		return ""
	}
	return match[1]
}
