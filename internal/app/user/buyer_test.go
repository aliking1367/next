package user

import "testing"

func TestBuyerTelegramIDReadsTheSalesBotNote(t *testing.T) {
	for note, want := range map[string]string{
		"New account created by 5683497583 at 09-11-2026 17:22:58": "5683497583",
		"new account CREATED BY 123456789":                         "123456789",
		"created by admin at 09-11-2026":                           "",
		"VIP customer, call before renewal":                        "",
		"":                                                         "",
	} {
		if got := BuyerTelegramID(note); got != want {
			t.Errorf("%q: got %q, want %q", note, got, want)
		}
	}
}
