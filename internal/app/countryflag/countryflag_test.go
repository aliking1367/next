package countryflag

import "testing"

func TestFlagBuildsRegionalIndicators(t *testing.T) {
	cases := map[string]string{
		"NL":   "🇳🇱",
		"nl":   "🇳🇱",
		" tr ": "🇹🇷",
		"SG":   "🇸🇬",
		"US":   "🇺🇸",
		// Not a country code, so no flag rather than a broken one.
		"":       "",
		"N":      "",
		"NLD":    "",
		"N1":     "",
		"🇳🇱": "",
	}
	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			if got := Flag(code); got != want {
				t.Fatalf("Flag(%q) = %q, want %q", code, got, want)
			}
		})
	}
}

// Both tables that used to exist are represented here: the English and city
// names one knew, and the Finglish and Persian spellings the other did.
func TestDetectReadsEnglishFinglishAndPersian(t *testing.T) {
	cases := map[string]string{
		"Germany":            "DE",
		"Alman 2":            "DE",
		"سرور آلمان":         "DE",
		"DE Frankfurt #2":    "DE",
		"Amsterdam":          "NL",
		"amsterdam-01":       "NL",
		"NL Amsterdam":       "NL",
		"هلند":               "NL",
		"hetzner-helsinki":   "FI",
		"Turkey":             "TR",
		"Türkiye Istanbul":   "TR",
		"ترکیه":              "TR",
		"paris":              "FR",
		"فرانسه":             "FR",
		"node-london-backup": "GB",
		"Singapore Gaming":   "SG",
		"new york":           "US",
		"آمریکا":             "US",
		"South Korea":        "KR",
		"South Africa":       "ZA",
		"ایران":              "IR",
		// A bare code on its own.
		"NL": "NL",
		"tr": "TR",
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Detect(name); got != want {
				t.Fatalf("Detect(%q) = %q, want %q", name, got, want)
			}
		})
	}
}

// A wrong flag misleads a user choosing a server by country in a way they
// cannot detect. No guess is the safer answer.
func TestDetectRefusesToGuess(t *testing.T) {
	for _, name := range []string{
		"coco", "server1", "king", "",
		// These used to be caught by substring matching.
		"checkout-node", "russian-bridge",
		// Two-letter country codes hiding inside ordinary words.
		"node in europe", "is this a node", "at home", "no name",
	} {
		t.Run(name, func(t *testing.T) {
			if got := Detect(name); got != "" {
				t.Fatalf("Detect(%q) = %q, want no guess", name, got)
			}
		})
	}
}

func TestHasLeadingFlag(t *testing.T) {
	cases := map[string]bool{
		"🇳🇱 Amsterdam": true,
		"🇹🇷Turkey":     true,
		"Amsterdam":        false,
		"":                 false,
		// One regional indicator is not a flag.
		"\U0001F1F3 Amsterdam": false,
		// A flag later in the name is not a leading one.
		"Amsterdam 🇳🇱": false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			if got := HasLeadingFlag(name); got != want {
				t.Fatalf("HasLeadingFlag(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		name     string
		fallback string
		want     string
	}{
		{"Germany", "203.0.113.1", "🇩🇪 Germany"},
		{"سرور آلمان", "203.0.113.1", "🇩🇪 سرور آلمان"},
		{"hetzner-helsinki", "203.0.113.1", "🇫🇮 hetzner-helsinki"},
		// A flag the admin wrote is kept, never doubled.
		{"🇺🇸 USA", "203.0.113.1", "🇺🇸 USA"},
		// Unknown place: the name alone.
		{"king", "203.0.113.1", "king"},
		// No name at all: whatever the caller has instead.
		{"", "203.0.113.1", "203.0.113.1"},
		{"   ", "203.0.113.1", "203.0.113.1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := Label(testCase.name, testCase.fallback); got != testCase.want {
				t.Fatalf("Label(%q) = %q, want %q", testCase.name, got, testCase.want)
			}
		})
	}
}

// Every alias must be reachable. A duplicate across two countries would make
// one of them unreachable depending on iteration order, which is the kind of
// thing that only shows up as "why does my node have the wrong flag".
func TestNoAliasIsClaimedByTwoCountries(t *testing.T) {
	owner := map[string]string{}
	for _, country := range countries {
		for _, alias := range country.Aliases {
			if previous, taken := owner[alias]; taken {
				t.Errorf("alias %q is claimed by both %s and %s", alias, previous, country.Code)
				continue
			}
			owner[alias] = country.Code
		}
	}
}

func TestEveryCountryCodeIsTwoLetters(t *testing.T) {
	for _, country := range countries {
		if Flag(country.Code) == "" {
			t.Errorf("%q is not a usable country code", country.Code)
		}
	}
}
