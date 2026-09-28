package user

import (
	"strings"
	"testing"
)

func TestBuildSubscriptionLinksAddsKeyUsernameFragment(t *testing.T) {
	links, err := BuildSubscriptionLinks(
		SubscriptionLinkRequest{
			Username:      "alice",
			CredentialKey: "credential-key",
			Preferred:     subscriptionTypeKeyUsername,
			Salt:          "fixed-salt",
		},
		SubscriptionSettings{},
		AdminLinkSettings{},
		"subscription-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "/sub/credential-key#alice"
	got, ok := links.Links.Get(subscriptionTypeKeyUsername)
	if !ok || got != want || links.Primary != want {
		t.Fatalf("key-username link = %q, primary = %q; want %q", got, links.Primary, want)
	}
}

// A domain that gets blocked must not cut a user off: every backup origin is
// handed out as its own link, so the client already holds a working one.
func TestBuildSubscriptionLinksAddsOneLinkPerBackupDomain(t *testing.T) {
	links, err := BuildSubscriptionLinks(
		SubscriptionLinkRequest{
			Username:      "alice",
			CredentialKey: "credential-key",
			Preferred:     subscriptionTypeKey,
			Salt:          "fixed-salt",
		},
		SubscriptionSettings{
			SubscriptionURLPrefix:      "https://panel.example.com:2053",
			SubscriptionBackupPrefixes: []string{"https://cdn.example.net", "static.example.org", "", "https://cdn.example.net/"},
		},
		AdminLinkSettings{},
		"subscription-secret",
	)
	if err != nil {
		t.Fatal(err)
	}
	// The primary link is untouched, so links already handed out keep working.
	if want := "https://panel.example.com:2053/sub/credential-key"; links.Primary != want {
		t.Fatalf("primary = %q, want %q", links.Primary, want)
	}
	for key, want := range map[string]string{
		"key@cdn.example.net":    "https://cdn.example.net/sub/credential-key",
		"key@static.example.org": "https://static.example.org/sub/credential-key",
	} {
		got, ok := links.Links.Get(key)
		if !ok || got != want {
			t.Errorf("%s = %q (present=%v), want %q", key, got, ok, want)
		}
	}
	// A bare host gained https, the blank entry was dropped and the repeated
	// origin was not offered twice.
	count := 0
	for _, key := range links.Links.keys {
		if strings.HasPrefix(key, "key@") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected exactly two backup links, got %d: %v", count, links.Links.keys)
	}
}

func TestNormalizeBackupPrefixesDropsBlanksAndDuplicates(t *testing.T) {
	got := normalizeBackupPrefixes([]string{" https://a.example.com/ ", "a.example.com", "", "https://b.example.com"})
	if len(got) != 2 || got[0] != "https://a.example.com" || got[1] != "https://b.example.com" {
		t.Fatalf("normalizeBackupPrefixes = %v", got)
	}
}
