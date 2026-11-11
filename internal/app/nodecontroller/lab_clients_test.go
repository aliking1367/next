package nodecontroller

import (
	"context"
	"testing"
)

func TestIncludeDBUsersKeepsOnlyTheLabsOwnTestClient(t *testing.T) {
	raw := map[string]any{"inbounds": []any{
		map[string]any{"tag": "tsin-lab-http", "protocol": "vless", "settings": map[string]any{
			"clients": []any{map[string]any{"id": "22222222-2222-4222-8222-222222222222", "email": "lab@tsin"}},
		}},
		map[string]any{"tag": "tsin-germany", "protocol": "vless", "settings": map[string]any{
			"clients": []any{map[string]any{"id": "33333333-3333-4333-8333-333333333333", "email": "stray"}},
		}},
	}}
	if err := (Controller{}).includeDBUsers(context.Background(), raw, &runtimeConfigData{}); err != nil {
		t.Fatal(err)
	}
	inbounds := listOfMaps(raw["inbounds"])
	lab := listOfMaps(ensureMap(inbounds[0], "settings")["clients"])
	if len(lab) != 1 || lab[0]["email"] != "lab@tsin" {
		t.Fatalf("the lab lost its test client: %#v", lab)
	}
	if other := listOfMaps(ensureMap(inbounds[1], "settings")["clients"]); len(other) != 0 {
		t.Fatalf("a stored client survived on a normal inbound: %#v", other)
	}
}
