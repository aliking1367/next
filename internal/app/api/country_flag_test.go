package api

import "testing"

// The table and the matching live in the countryflag package and are tested
// there. What belongs here is the label this package builds from them.
func TestCDNHostRemarkPutsTheFlagInFront(t *testing.T) {
	cases := map[string]string{
		"Amsterdam": "🇳🇱 Amsterdam · CDN",
		"Paris":     "🇫🇷 Paris · CDN",
		"Turkey":    "🇹🇷 Turkey · CDN",
		// Now shared with the multi-location labels, so Persian works too.
		"آلمان": "🇩🇪 آلمان · CDN",
		// A flag the admin wrote is kept, never doubled.
		"🇳🇱 Amsterdam": "🇳🇱 Amsterdam · CDN",
		// Unknown place: the name alone, never a wrong flag.
		"coco": "coco · CDN",
		// A nameless node still gets a usable label rather than " · CDN".
		"":    "node · CDN",
		"   ": "node · CDN",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := cdnHostRemark(name); got != want {
				t.Fatalf("cdnHostRemark(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

// The remark feeds the host row, and cdnHostLabel feeds the DNS name from the
// same node name. A hostname with an emoji in it would resolve nowhere.
func TestFlagNeverReachesTheDNSLabel(t *testing.T) {
	used := map[string]bool{}
	if label := cdnHostLabel("🇳🇱 Amsterdam", 1, used); label != "amsterdam" {
		t.Fatalf("cdnHostLabel = %q, want %q", label, "amsterdam")
	}
}
