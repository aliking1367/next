package user

import (
	"strings"
	"testing"
)

// The subscription page writes the links into a <script> block through
// `{{ links_text|safe }}`, which turns off the template's own escaping. So the
// value handed to it has to be safe on its own: a link holding "</script>"
// would otherwise close the block and run whatever came after it, on every
// user's subscription page, stored.
func TestLegacyTemplateStringListCannotCloseAScriptBlock(t *testing.T) {
	payload := `vless://x@example.com:443#</script><script>alert(1)</script>`
	rendered := legacyTemplateStringList([]string{payload})

	if strings.Contains(strings.ToLower(rendered), "</script") {
		t.Fatalf("the rendered literal can close the script block: %s", rendered)
	}
	if strings.ContainsAny(rendered, "<>") {
		t.Fatalf("the rendered literal still carries angle brackets: %s", rendered)
	}
	// It must still be the array the page reads, not an empty one.
	if !strings.HasPrefix(rendered, "[") || !strings.HasSuffix(rendered, "]") {
		t.Fatalf("not an array literal: %s", rendered)
	}
	if !strings.Contains(rendered, "example.com") {
		t.Fatalf("the link itself was lost: %s", rendered)
	}
}

func TestLegacyTemplateStringListSurvivesQuotesAndNewlines(t *testing.T) {
	rendered := legacyTemplateStringList([]string{
		`vless://x@example.com:443#it's "quoted"`,
		"vless://y@example.com:443#two\nlines",
		`vless://z@example.com:443#back\slash`,
	})
	if strings.Contains(rendered, "\n") {
		t.Fatalf("a raw newline would break the literal: %q", rendered)
	}
	for _, want := range []string{"example.com", `\"`, `\n`, `\\`} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in %s", want, rendered)
		}
	}
}

func TestLegacyTemplateStringListEmpty(t *testing.T) {
	if got := legacyTemplateStringList(nil); got != "[]" {
		t.Fatalf("legacyTemplateStringList(nil) = %q, want []", got)
	}
}

// Rejecting these at the door as well: a stored link is shown in places other
// than the page this file escapes for.
func TestExtraLinksRejectMarkupAndControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		link string
	}{
		{"closing script tag", `vless://x@example.com:443#</script><script>alert(1)</script>`},
		{"angle bracket", `vless://x@example.com:443#<img`},
		{"double quote", `vless://x@example.com:443#say"hi`},
		{"tab", "vless://x@example.com:443#a\tb"},
		{"null byte", "vless://x@example.com:443#a\x00b"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if isShareLink(testCase.link) {
				t.Fatalf("isShareLink accepted %q", testCase.link)
			}
			if got := NormalizeExtraLinks([]string{testCase.link}); got != "" {
				t.Fatalf("NormalizeExtraLinks kept %q as %q", testCase.link, got)
			}
		})
	}
}

// The ordinary links an admin actually pastes must still go through, or the
// check above would have quietly turned the feature off.
func TestExtraLinksStillAcceptRealLinks(t *testing.T) {
	links := []string{
		"vless://3704b0ea-0965-fa12-96a0-a02b2d648f0a@coco.example.com:2087?encryption=none&security=tls&type=xhttp#Backup",
		"hysteria2://user@example.com:45927/?alpn=h3#Gaming",
		"ss://YWVzOnBhc3M@example.com:8388#Fallback",
		"wireguard://key@example.com:51820#WG",
	}
	normalized := NormalizeExtraLinks(links)
	parsed := ParseExtraLinks(normalized)
	if len(parsed) != len(links) {
		t.Fatalf("kept %d of %d links: %v", len(parsed), len(links), parsed)
	}
	for i, link := range links {
		if parsed[i] != link {
			t.Fatalf("link %d changed:\n got %q\nwant %q", i, parsed[i], link)
		}
	}
}
