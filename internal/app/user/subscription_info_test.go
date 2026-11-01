package user

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func vmessRemark(t *testing.T, link string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("not a vmess link: %s", link)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["add"] != "127.0.0.1" || payload["port"] != "1" {
		t.Fatalf("an info entry must not point anywhere usable: %v", payload)
	}
	return payload["ps"].(string)
}

func TestSubscriptionInfoLinksShowTheUsersOwnNumbers(t *testing.T) {
	limit := int64(100 << 30)
	user := UserDetail{ID: 1, Username: "sajjad", Status: "active", UsedTraffic: 40 << 30, DataLimit: &limit}

	if links := subscriptionInfoLinks(user, SubscriptionSettings{}, "v2ray"); links != nil {
		t.Fatalf("info lines appeared while switched off: %v", links)
	}
	on := SubscriptionSettings{SubscriptionInfoEnabled: true}
	if links := subscriptionInfoLinks(user, on, "clash-meta"); links != nil {
		t.Fatalf("info lines leaked into a non-link format: %v", links)
	}

	links := subscriptionInfoLinks(user, on, "v2ray")
	if len(links) != len(defaultSubscriptionInfoRemarks) {
		t.Fatalf("want one entry per default line, got %d", len(links))
	}
	joined := ""
	for _, link := range links {
		joined += vmessRemark(t, link) + "\n"
	}
	for _, want := range []string{"sajjad", "60", "100", "\U0001F7E9", "⬜"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "{") {
		t.Fatalf("an unreplaced variable reached the user:\n%s", joined)
	}

	custom := SubscriptionSettings{SubscriptionInfoEnabled: true, SubscriptionInfoRemarks: "Hi {USERNAME}\n\n  \nLeft {DATA_LEFT}"}
	links = subscriptionInfoLinks(user, custom, "v2ray")
	if len(links) != 2 || vmessRemark(t, links[0]) != "Hi sajjad" {
		t.Fatalf("custom lines = %v", links)
	}
}

func TestRemainingBarColoursByShareLeft(t *testing.T) {
	for _, tc := range []struct {
		remaining, limit          int64
		green, yellow, red, empty int
	}{
		{100, 100, 10, 0, 0, 0},
		{50, 100, 0, 5, 0, 5},
		{10, 100, 0, 0, 1, 9},
		{0, 100, 0, 0, 0, 10},
	} {
		bar := remainingBar(tc.remaining, tc.limit)
		got := [4]int{strings.Count(bar, "\U0001F7E9"), strings.Count(bar, "\U0001F7E8"), strings.Count(bar, "\U0001F7E5"), strings.Count(bar, "⬜")}
		if got != [4]int{tc.green, tc.yellow, tc.red, tc.empty} {
			t.Fatalf("remainingBar(%d,%d) = %q", tc.remaining, tc.limit, bar)
		}
	}
	if remainingBar(5, 0) != "∞" {
		t.Fatal("an unlimited user must not get a bar")
	}
}
