package user

import (
	"strings"
	"testing"
)

// What an admin pastes is rarely clean: several links at once, blank lines,
// a repeat, and often a note or a web address that no client could import.
func TestNormalizeExtraLinksKeepsOnlyUsableConfigs(t *testing.T) {
	got := NormalizeExtraLinks([]string{
		"  vless://uuid@example.com:443?type=tcp#one  ",
		"vmess://base64payload\ntrojan://pass@example.net:443#two",
		"",
		"vless://uuid@example.com:443?type=tcp#one",
		"just a note from the admin",
		"https://example.com/page",
		"example.com:443",
		"hysteria2://pass@example.org:443#three",
	})
	links := strings.Split(got, "\n")
	want := []string{
		"vless://uuid@example.com:443?type=tcp#one",
		"vmess://base64payload",
		"trojan://pass@example.net:443#two",
		"hysteria2://pass@example.org:443#three",
	}
	if len(links) != len(want) {
		t.Fatalf("normalized = %#v, want %#v", links, want)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, links[i], want[i])
		}
	}
}

func TestNormalizeExtraLinksHasABound(t *testing.T) {
	many := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, "vless://uuid@example.com:443#"+strings.Repeat("x", i+1))
	}
	if got := len(ParseExtraLinks(NormalizeExtraLinks(many))); got != maxExtraLinksPerUser {
		t.Fatalf("stored %d links, want the cap of %d", got, maxExtraLinksPerUser)
	}
	long := strings.Repeat("y", maxExtraLinkLength+1)
	if NormalizeExtraLinks([]string{"vless://" + long}) != "" {
		t.Error("an oversized link must not be stored")
	}
}

// The admin's configs have to reach the client, and must not disturb the
// panel's own ones or the metadata that is index-aligned with them.
func TestAppendExtraLinksKeepsMetadataAligned(t *testing.T) {
	connectable := ConfigLinksResponse{
		Links:    []string{"vless://panel-one", "vless://panel-two"},
		Metadata: []ConfigLinkMetadata{{}, {}},
	}
	got := appendExtraLinks(connectable, []string{"trojan://manual", "  ", "hysteria2://manual"})
	if len(got.Links) != 4 {
		t.Fatalf("links = %v", got.Links)
	}
	if got.Links[0] != "vless://panel-one" || got.Links[1] != "vless://panel-two" {
		t.Errorf("the panel's own links must stay first and unchanged: %v", got.Links)
	}
	if got.Links[2] != "trojan://manual" || got.Links[3] != "hysteria2://manual" {
		t.Errorf("manual links = %v", got.Links[2:])
	}
	if len(got.Metadata) != len(got.Links) {
		t.Errorf("metadata (%d) must stay aligned with links (%d)", len(got.Metadata), len(got.Links))
	}
}

func TestAppendExtraLinksIsANoOpWithoutAny(t *testing.T) {
	connectable := ConfigLinksResponse{Links: []string{"vless://one"}, Metadata: []ConfigLinkMetadata{{}}}
	got := appendExtraLinks(connectable, nil)
	if len(got.Links) != 1 || len(got.Metadata) != 1 {
		t.Fatalf("unexpected change: %+v", got)
	}
}
