package nodecontroller

import (
	"context"
	"database/sql"
	"testing"
)

// A hidden host shown to one admin's users lets only those users into its
// inbound; everyone else stays out, as with any hidden host.
func TestIncludeDBUsersLetsOnlyTheChosenAdminsUsersIntoAHiddenHost(t *testing.T) {
	raw := jsonMap(`{"inbounds":[
		{"tag":"emergency-vmess","protocol":"vmess","settings":{"clients":[]}},
		{"tag":"tsin-germany","protocol":"vmess","settings":{"clients":[]}}
	]}`)
	key := "0123456789abcdef0123456789abcdef"
	user := func(id, admin int64) runtimeUserRow {
		return runtimeUserRow{
			ID: id, Username: "u" + string(rune('0'+id)), CredentialKey: key, Protocol: "vmess",
			ServiceID: sql.NullInt64{Int64: 1, Valid: true}, AdminID: sql.NullInt64{Int64: admin, Valid: true},
		}
	}
	data := &runtimeConfigData{
		users:       []runtimeUserRow{user(1, 5), user(2, 6)},
		serviceTags: map[int64]map[string]bool{1: {"tsin-germany": true}},
		adminTags:   adminAllowedTags{1: {5: {"emergency-vmess": true}}},
	}
	if err := (Controller{}).includeDBUsers(context.Background(), raw, data); err != nil {
		t.Fatal(err)
	}
	clients := func(tag string) int {
		for _, inbound := range listOfMaps(raw["inbounds"]) {
			if stringValue(inbound["tag"]) == tag {
				return len(interfaceSlice(mapValue(inbound["settings"])["clients"]))
			}
		}
		return -1
	}
	if got := clients("emergency-vmess"); got != 1 {
		t.Fatalf("emergency inbound has %d clients, want only admin 5's user", got)
	}
	if got := clients("tsin-germany"); got != 2 {
		t.Fatalf("an ordinary inbound lost users: %d", got)
	}

	// Without any per-admin entry nobody gets the hidden host.
	raw = jsonMap(`{"inbounds":[{"tag":"emergency-vmess","protocol":"vmess","settings":{"clients":[]}}]}`)
	data.adminTags = nil
	if err := (Controller{}).includeDBUsers(context.Background(), raw, data); err != nil {
		t.Fatal(err)
	}
	if got := len(interfaceSlice(mapValue(listOfMaps(raw["inbounds"])[0]["settings"])["clients"])); got != 0 {
		t.Fatalf("a hidden host let %d users in", got)
	}
}
