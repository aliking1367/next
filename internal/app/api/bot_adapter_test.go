package api

import (
	"testing"

	userapp "github.com/aliking1367/next/internal/app/user"
)

// An admin forwards what the bot prints, so it must show the same
// subscription type on every backup domain: one link per domain, and none of
// the other link formats.
func TestBackupSubscriptionURLsPicksThePrimaryTypeOnEachDomain(t *testing.T) {
	urls := userapp.NewOrderedStringMap(6)
	urls.Set("username-key", "https://panel.example.com/sub/alice/key")
	urls.Set("key", "https://panel.example.com/sub/key")
	urls.Set("username-key@cdn.example.net", "https://cdn.example.net/sub/alice/key")
	urls.Set("key@cdn.example.net", "https://cdn.example.net/sub/key")
	urls.Set("key-username@cdn.example.net", "https://cdn.example.net/sub/key#alice")
	urls.Set("key@static.example.org", "https://static.example.org/sub/key")

	detail := userapp.UserDetail{
		SubscriptionURL:  "https://panel.example.com/sub/key",
		SubscriptionURLs: urls,
	}
	got := backupSubscriptionURLs(detail)
	want := []string{"https://cdn.example.net/sub/key", "https://static.example.org/sub/key"}
	if len(got) != len(want) {
		t.Fatalf("backupSubscriptionURLs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBackupSubscriptionURLsIsEmptyWithoutBackupDomains(t *testing.T) {
	urls := userapp.NewOrderedStringMap(2)
	urls.Set("key", "https://panel.example.com/sub/key")
	detail := userapp.UserDetail{
		SubscriptionURL:  "https://panel.example.com/sub/key",
		SubscriptionURLs: urls,
	}
	if got := backupSubscriptionURLs(detail); len(got) != 0 {
		t.Fatalf("backupSubscriptionURLs = %v, want none", got)
	}
}
