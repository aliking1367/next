package api

import "testing"

func TestCountryFlagBuildsRegionalIndicators(t *testing.T) {
	cases := map[string]string{
		"NL": "🇳🇱",
		"nl": "🇳🇱",
		" tr ": "🇹🇷",
		"SG": "🇸🇬",
		"US": "🇺🇸",
		// Not a country code, so no flag rather than a broken one.
		"":     "",
		"N":    "",
		"NLD":  "",
		"N1":   "",
		"🇳🇱": "",
	}
	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			if got := countryFlag(code); got != want {
				t.Fatalf("countryFlag(%q) = %q, want %q", code, got, want)
			}
		})
	}
}

func TestDetectCountryCodeReadsPlacesFromNodeNames(t *testing.T) {
	cases := map[string]string{
		"Amsterdam":          "NL",
		"amsterdam-01":       "NL",
		"Paris":              "FR",
		"Turkey":             "TR",
		"Türkiye Istanbul":   "TR",
		"Singapore Gaming":   "SG",
		"DE Frankfurt #2":    "DE",
		"new york":           "US",
		"South Korea":        "KR",
		"South Africa":       "ZA",
		"🇳🇱 Amsterdam":    "NL",
		"node-london-backup": "GB",
		// Nothing recognisable: no guess.
		"coco":    "",
		"server1": "",
		"":        "",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := detectCountryCode(name); got != want {
				t.Fatalf("detectCountryCode(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

// A two-letter word inside a longer name is far too easy to hit by accident --
// "is", "at", "in", "no" and "my" are all country codes. They only count when
// the name is nothing else.
func TestDetectCountryCodeIgnoresShortWordsInsideLongerNames(t *testing.T) {
	for _, name := range []string{"node in europe", "is this a node", "at home", "no name"} {
		t.Run(name, func(t *testing.T) {
			if got := detectCountryCode(name); got != "" {
				t.Fatalf("detectCountryCode(%q) = %q, want no guess", name, got)
			}
		})
	}
	// On its own it is clearly meant as the code.
	if got := detectCountryCode("NL"); got != "NL" {
		t.Fatalf("detectCountryCode(\"NL\") = %q", got)
	}
}

func TestHasLeadingFlag(t *testing.T) {
	cases := map[string]bool{
		"🇳🇱 Amsterdam": true,
		"🇹🇷Turkey":     true,
		"Amsterdam":       false,
		"":                false,
		// One regional indicator is not a flag.
		"\U0001F1F3 Amsterdam": false,
		// A flag later in the name is not a leading one.
		"Amsterdam 🇳🇱": false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := hasLeadingFlag(name); got != want {
				t.Fatalf("hasLeadingFlag(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestCDNHostRemarkPutsTheFlagInFront(t *testing.T) {
	cases := map[string]string{
		"Amsterdam": "🇳🇱 Amsterdam · CDN",
		"Paris":     "🇫🇷 Paris · CDN",
		"Turkey":    "🇹🇷 Turkey · CDN",
		// A flag the admin wrote is kept, never doubled.
		"🇳🇱 Amsterdam": "🇳🇱 Amsterdam · CDN",
		// Unknown place: the name alone, never a wrong flag.
		"coco": "coco · CDN",
		// A nameless node still gets a usable label.
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
// same node name. The flag must not leak into the hostname.
func TestFlagNeverReachesTheDNSLabel(t *testing.T) {
	used := map[string]bool{}
	label := cdnHostLabel("🇳🇱 Amsterdam", 1, used)
	if label != "amsterdam" {
		t.Fatalf("cdnHostLabel = %q, want %q", label, "amsterdam")
	}
}
