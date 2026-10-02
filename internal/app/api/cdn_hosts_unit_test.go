package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// Node names carry flags, punctuation and spaces that no DNS label may hold.
func TestCDNHostLabelMakesADNSLabel(t *testing.T) {
	used := map[string]bool{}
	for _, item := range []struct{ name, want string }{
		{"SG, Singapore (GAMING 🎮)", "sg-singapore-gaming"},
		{"🇳🇱 Amsterdam", "amsterdam"},
		{"paris", "paris"},
		{"Turkey   node", "turkey-node"},
		{"🎮🎮🎮", "node"},
		{strings.Repeat("a", 60), strings.Repeat("a", 40)},
	} {
		if got := cdnHostLabel(item.name, 1, used); got != item.want {
			t.Errorf("cdnHostLabel(%q) = %q, want %q", item.name, got, item.want)
		}
		delete(used, item.want)
	}
}

// Two nodes whose names reduce to the same label must not be handed the same
// hostname: one of them would silently never be reachable.
func TestCDNHostLabelKeepsCollisionsApart(t *testing.T) {
	used := map[string]bool{}
	first := cdnHostLabel("Amsterdam", 4, used)
	second := cdnHostLabel("amsterdam!", 7, used)
	if first == second {
		t.Fatalf("both nodes got %q", first)
	}
	if !strings.HasSuffix(second, "-7") {
		t.Errorf("the second label = %q, want it marked with the node id", second)
	}
}

func TestNormalizeCDNSuffixAcceptsWhatAdminsPaste(t *testing.T) {
	for input, want := range map[string]string{
		"example.com":          "example.com",
		"  EXAMPLE.com  ":      "example.com",
		"https://example.com/": "example.com",
		"http://example.com":   "example.com",
		".example.com.":        "example.com",
		"sub.example.com":      "sub.example.com",
		"":                     "",
		"localhost":            "",
		"not a domain":         "",
		"例え.com":               "",
	} {
		if got := normalizeCDNSuffix(input); got != want {
			t.Errorf("normalizeCDNSuffix(%q) = %q, want %q", input, got, want)
		}
	}
}

// The token can edit the admin's whole zone, so it must not come back in any
// response or be kept on the request struct after use.
func TestCDNHostsRequestDoesNotEchoTheToken(t *testing.T) {
	encoded, err := json.Marshal(cdnHostsResponse{
		InboundTag: "auto-cdn-xhttp",
		Hosts: []cdnHostPlan{{
			NodeName: "Amsterdam",
			NodeIP:   "203.0.113.10",
			Hostname: "amsterdam.example.com",
		}},
		Created:    1,
		DNSManaged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "cloudflare_token", "Bearer"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Errorf("the response mentions %q: %s", forbidden, encoded)
		}
	}
}

// Cloudflare's own failure text reaches the admin, but nothing that could
// carry the token with it.
func TestCloudflareErrorCarriesOnlyItsMessage(t *testing.T) {
	err := cloudflareError{Message: "the token was refused; it needs Zone:Read and DNS:Edit on this zone"}
	if !strings.Contains(err.Error(), "Zone:Read") {
		t.Errorf("error text = %q", err.Error())
	}
	if strings.Contains(err.Error(), "Bearer") {
		t.Error("an error must never repeat the authorization header")
	}
}
