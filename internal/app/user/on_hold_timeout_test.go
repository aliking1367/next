package user

import (
	"strings"
	"testing"
)

// on_hold_timeout is a DATETIME column, and the value used to be bound to it
// exactly as the caller sent it. A Marzban-era client sends RFC 3339, which
// MySQL refuses outright, so creating a user failed with "Incorrect datetime
// value" and a reseller bot could not create a single one. SQLite accepts the
// same string, which is why it only showed up in production.
func TestNormalizeOnHoldTimeoutAcceptsWhatClientsActuallySend(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"rfc3339 zulu", "2026-10-03T13:49:08Z", "2026-10-03 13:49:08"},
		{"rfc3339 with offset", "2026-10-03T17:19:08+03:30", "2026-10-03 13:49:08"},
		{"rfc3339 fractional", "2026-10-03T13:49:08.123456Z", "2026-10-03 13:49:08"},
		{"no zone", "2026-10-03T13:49:08", "2026-10-03 13:49:08"},
		{"already a datetime", "2026-10-03 13:49:08", "2026-10-03 13:49:08"},
		// Marzban stored this column as a Unix timestamp, so a client ported
		// from it sends a bare number.
		{"unix seconds", "1791035348", "2026-10-03 13:49:08"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := normalizeOnHoldTimeout(&testCase.value)
			if err != nil {
				t.Fatalf("normalizeOnHoldTimeout(%q) returned %v", testCase.value, err)
			}
			text, ok := got.(string)
			if !ok {
				t.Fatalf("normalizeOnHoldTimeout(%q) = %#v, want a string", testCase.value, got)
			}
			if text != testCase.want {
				t.Fatalf("normalizeOnHoldTimeout(%q) = %q, want %q", testCase.value, text, testCase.want)
			}
		})
	}
}

func TestNormalizeOnHoldTimeoutTreatsAbsenceAsNull(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value *string
	}{
		{"nil", nil},
		{"empty", stringPointer("")},
		{"blank", stringPointer("   ")},
		// A zero or negative timestamp is "not set", not 1970.
		{"zero unix", stringPointer("0")},
		{"negative unix", stringPointer("-1")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := normalizeOnHoldTimeout(testCase.value)
			if err != nil {
				t.Fatalf("returned %v", err)
			}
			if got != nil {
				t.Fatalf("got %#v, want nil", got)
			}
		})
	}
}

// An unparseable value has to come back as a bad request. Letting it reach the
// column means the database raises its own error, the caller gets a 502 with
// driver text in it, and nobody can tell which field was wrong.
func TestNormalizeOnHoldTimeoutRejectsNonsenseAsABadRequest(t *testing.T) {
	value := "next tuesday"
	got, err := normalizeOnHoldTimeout(&value)
	if err == nil {
		t.Fatalf("expected an error, got %#v", got)
	}
	if !strings.Contains(err.Error(), "on_hold_timeout") {
		t.Fatalf("the error should name the field: %v", err)
	}
	mutationErr, ok := err.(MutationError)
	if !ok {
		t.Fatalf("expected a MutationError, got %T", err)
	}
	if mutationErr.Status != 400 {
		t.Fatalf("status = %d, want 400", mutationErr.Status)
	}
}

func stringPointer(value string) *string { return &value }
