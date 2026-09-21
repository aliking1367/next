package user

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestSubscriptionProfileTitleFillsUserPlaceholders(t *testing.T) {
	decode := func(header string) string {
		t.Helper()
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "base64:"))
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	user := UserDetail{Username: "alice", Status: "active"}

	headers := subscriptionHeaders(user, SubscriptionRenderRequest{}, SubscriptionSettings{SubscriptionProfileTitle: "{USERNAME} VPN"})
	if got := decode(headers["profile-title"]); got != "alice VPN" {
		t.Fatalf("title = %q, want %q", got, "alice VPN")
	}

	headers = subscriptionHeaders(user, SubscriptionRenderRequest{}, SubscriptionSettings{})
	if got := decode(headers["profile-title"]); got != "Subscription" {
		t.Fatalf("default title = %q", got)
	}
}
