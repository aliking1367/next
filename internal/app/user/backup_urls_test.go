package user

import "testing"

func buildURLs(pairs ...[2]string) OrderedStringMap {
	m := NewOrderedStringMap(len(pairs))
	for _, pair := range pairs {
		m.Set(pair[0], pair[1])
	}
	return m
}

// One row per origin, not one per link style: the page lists addresses to fall
// back to, and four styles of the same address hides the point.
func TestBackupSubscriptionURLsGiveOneRowPerOrigin(t *testing.T) {
	user := UserDetail{SubscriptionURLs: buildURLs(
		[2]string{"username-key", "https://panel.example.com:2053/sub/alice/k"},
		[2]string{"key", "https://panel.example.com:2053/sub/k"},
		[2]string{"token", "https://panel.example.com:2053/sub/t"},
		[2]string{"username-key@a.example.com", "https://a.example.com:2053/sub/alice/k"},
		[2]string{"key@a.example.com", "https://a.example.com:2053/sub/k"},
		[2]string{"token@a.example.com", "https://a.example.com:2053/sub/t"},
		[2]string{"key@b.example.com", "https://b.example.com:2053/sub/k"},
		[2]string{"token@b.example.com", "https://b.example.com:2053/sub/t"},
	)}
	got := backupSubscriptionURLs(user)
	if len(got) != 2 {
		t.Fatalf("expected one row per origin, got %d: %v", len(got), got)
	}
	// "key" exists on the primary, so the spares are the same kind of link the
	// user was already given.
	if got[0]["url"] != "https://a.example.com:2053/sub/k" {
		t.Fatalf("first row = %v", got[0])
	}
	if got[1]["url"] != "https://b.example.com:2053/sub/k" {
		t.Fatalf("second row = %v", got[1])
	}
	if got[0]["label"] != "a.example.com" {
		t.Fatalf("label = %v", got[0]["label"])
	}
}

// A user without a credential key has only token links; the spares must follow.
func TestBackupSubscriptionURLsFollowTheAvailableStyle(t *testing.T) {
	user := UserDetail{SubscriptionURLs: buildURLs(
		[2]string{"token", "https://panel.example.com:2053/sub/t"},
		[2]string{"token@a.example.com", "https://a.example.com:2053/sub/t"},
	)}
	got := backupSubscriptionURLs(user)
	if len(got) != 1 || got[0]["url"] != "https://a.example.com:2053/sub/t" {
		t.Fatalf("got %v", got)
	}
}

func TestBackupSubscriptionURLsEmptyWithoutBackups(t *testing.T) {
	user := UserDetail{SubscriptionURLs: buildURLs(
		[2]string{"key", "https://panel.example.com:2053/sub/k"},
	)}
	if got := backupSubscriptionURLs(user); got != nil {
		t.Fatalf("got %v, want nothing", got)
	}
	if got := backupSubscriptionURLs(UserDetail{}); got != nil {
		t.Fatalf("got %v, want nothing", got)
	}
}
