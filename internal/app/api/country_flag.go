package api

import (
	"strings"
	"unicode"
)

// A user picking a config from a list reads the flag before the words. The
// panel builds the CDN host rows itself, so it is the panel that has to put the
// flag there -- "🇳🇱 Amsterdam · CDN" rather than "Amsterdam · CDN".
//
// The country is worked out from the node's name, because that is the only
// place the panel is told where a node is. Three things can happen, and the
// order matters:
//
//  1. The name already starts with a flag, which is how an admin writes it when
//     they care. That flag is kept and never replaced or doubled.
//  2. The name contains a place this knows. Its flag is used.
//  3. Neither. No flag at all -- a wrong flag is worse than none, because a
//     user picking a server by country would be misled by it, and the admin can
//     always put the flag in the node's name themselves.

// countryFlag turns a two-letter ISO country code into its flag, which is the
// two matching regional indicator symbols.
func countryFlag(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 {
		return ""
	}
	runes := make([]rune, 0, 2)
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return ""
		}
		runes = append(runes, rune(0x1F1E6+(r-'A')))
	}
	return string(runes)
}

// hasLeadingFlag reports whether a name already begins with a flag, so one the
// admin wrote is left alone.
func hasLeadingFlag(name string) bool {
	count := 0
	for _, r := range strings.TrimSpace(name) {
		if r < 0x1F1E6 || r > 0x1F1FF {
			break
		}
		count++
		if count == 2 {
			return true
		}
	}
	return false
}

// countryByPlace maps what admins actually call their nodes -- a country, a
// city, or an airport-style abbreviation -- to a country code. It covers the
// locations providers sell rather than every country on earth; anything missing
// simply gets no flag.
var countryByPlace = map[string]string{
	// Europe
	"netherlands": "NL", "holland": "NL", "amsterdam": "NL", "nl": "NL", "ams": "NL",
	"germany": "DE", "deutschland": "DE", "frankfurt": "DE", "berlin": "DE", "munich": "DE", "de": "DE", "fra": "DE",
	"france": "FR", "paris": "FR", "marseille": "FR", "gravelines": "FR", "fr": "FR", "par": "FR",
	"england": "GB", "britain": "GB", "london": "GB", "manchester": "GB", "uk": "GB", "gb": "GB", "lon": "GB",
	"finland": "FI", "helsinki": "FI", "fi": "FI",
	"sweden": "SE", "stockholm": "SE", "se": "SE",
	"norway": "NO", "oslo": "NO", "no": "NO",
	"denmark": "DK", "copenhagen": "DK", "dk": "DK",
	"poland": "PL", "warsaw": "PL", "pl": "PL",
	"austria": "AT", "vienna": "AT", "at": "AT",
	"switzerland": "CH", "zurich": "CH", "geneva": "CH", "ch": "CH",
	"spain": "ES", "madrid": "ES", "barcelona": "ES", "es": "ES",
	"italy": "IT", "milan": "IT", "rome": "IT", "it": "IT",
	"ireland": "IE", "dublin": "IE", "ie": "IE",
	"belgium": "BE", "brussels": "BE", "be": "BE",
	"portugal": "PT", "lisbon": "PT", "pt": "PT",
	"czechia": "CZ", "czech": "CZ", "prague": "CZ", "cz": "CZ",
	"romania": "RO", "bucharest": "RO", "ro": "RO",
	"bulgaria": "BG", "sofia": "BG", "bg": "BG",
	"hungary": "HU", "budapest": "HU", "hu": "HU",
	"latvia": "LV", "riga": "LV", "lv": "LV",
	"lithuania": "LT", "vilnius": "LT", "lt": "LT",
	"estonia": "EE", "tallinn": "EE", "ee": "EE",
	"ukraine": "UA", "kyiv": "UA", "kiev": "UA", "ua": "UA",
	"russia": "RU", "moscow": "RU", "ru": "RU",
	"moldova": "MD", "chisinau": "MD", "md": "MD",
	"serbia": "RS", "belgrade": "RS", "rs": "RS",
	"greece": "GR", "athens": "GR", "gr": "GR",
	"iceland": "IS", "reykjavik": "IS", "is": "IS",
	"luxembourg": "LU", "lu": "LU",

	// Middle East and nearby
	"turkey": "TR", "turkiye": "TR", "istanbul": "TR", "ankara": "TR", "izmir": "TR", "tr": "TR", "ist": "TR",
	"iran": "IR", "tehran": "IR", "ir": "IR",
	"emirates": "AE", "dubai": "AE", "abudhabi": "AE", "uae": "AE", "ae": "AE",
	"qatar": "QA", "doha": "QA", "qa": "QA",
	"israel": "IL", "telaviv": "IL", "il": "IL",
	"armenia": "AM", "yerevan": "AM", "am": "AM",
	"georgia": "GE", "tbilisi": "GE", "ge": "GE",
	"azerbaijan": "AZ", "baku": "AZ", "az": "AZ",
	"kazakhstan": "KZ", "almaty": "KZ", "kz": "KZ",
	"cyprus": "CY", "cy": "CY",
	"bahrain": "BH", "bh": "BH",
	"kuwait": "KW", "kw": "KW",
	"oman": "OM", "muscat": "OM", "om": "OM",
	"saudi": "SA", "riyadh": "SA", "jeddah": "SA", "sa": "SA",

	// Asia and Oceania
	"singapore": "SG", "sg": "SG", "sin": "SG",
	"japan": "JP", "tokyo": "JP", "osaka": "JP", "jp": "JP", "nrt": "JP",
	"korea": "KR", "southkorea": "KR", "seoul": "KR", "kr": "KR",
	"hongkong": "HK", "hk": "HK",
	"taiwan": "TW", "taipei": "TW", "tw": "TW",
	"china": "CN", "shanghai": "CN", "beijing": "CN", "cn": "CN",
	"india": "IN", "mumbai": "IN", "delhi": "IN", "bangalore": "IN", "in": "IN",
	"indonesia": "ID", "jakarta": "ID", "id": "ID",
	"malaysia": "MY", "kualalumpur": "MY", "my": "MY",
	"thailand": "TH", "bangkok": "TH", "th": "TH",
	"vietnam": "VN", "hanoi": "VN", "saigon": "VN", "vn": "VN",
	"philippines": "PH", "manila": "PH", "ph": "PH",
	"australia": "AU", "sydney": "AU", "melbourne": "AU", "au": "AU", "syd": "AU",
	"newzealand": "NZ", "auckland": "NZ", "nz": "NZ",

	// Americas and Africa
	"usa": "US", "unitedstates": "US", "america": "US", "us": "US",
	"newyork": "US", "losangeles": "US", "dallas": "US", "miami": "US",
	"chicago": "US", "seattle": "US", "ashburn": "US", "atlanta": "US",
	"canada": "CA", "toronto": "CA", "montreal": "CA", "vancouver": "CA", "ca": "CA",
	"brazil": "BR", "saopaulo": "BR", "br": "BR",
	"argentina": "AR", "buenosaires": "AR", "ar": "AR",
	"chile": "CL", "santiago": "CL", "cl": "CL",
	"mexico": "MX", "mx": "MX",
	"southafrica": "ZA", "johannesburg": "ZA", "capetown": "ZA", "za": "ZA",
	"egypt": "EG", "cairo": "EG", "eg": "EG",
	"nigeria": "NG", "lagos": "NG", "ng": "NG",
	"kenya": "KE", "nairobi": "KE", "ke": "KE",
}

// detectCountryCode finds a place in a node's name. It walks the name word by
// word and also tries adjacent pairs, so "new york" and "south korea" are found
// as readily as "amsterdam".
func detectCountryCode(name string) string {
	words := splitNameWords(name)
	// Pairs first: "south korea" must not be read as "korea" by luck, nor
	// "south africa" as "africa".
	for i := 0; i+1 < len(words); i++ {
		if code, ok := countryByPlace[words[i]+words[i+1]]; ok {
			return code
		}
	}
	for _, word := range words {
		// A bare one- or two-letter word is too easy to hit by accident, so
		// short forms only count when the name is just that.
		if len(word) < 3 && len(words) > 1 {
			continue
		}
		if code, ok := countryByPlace[word]; ok {
			return code
		}
	}
	return ""
}

// splitNameWords reduces a node name to lowercase ASCII words, dropping flags,
// punctuation and anything else admins decorate names with.
func splitNameWords(name string) []string {
	var current strings.Builder
	words := make([]string, 0, 4)
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, r := range strings.ToLower(name) {
		if r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			current.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return words
}

// cdnHostRemark is the label a user sees for a CDN config.
func cdnHostRemark(nodeName string) string {
	name := strings.TrimSpace(nodeName)
	if name == "" {
		name = "node"
	}
	if hasLeadingFlag(name) {
		// The admin already said which country this is.
		return name + " · CDN"
	}
	if flag := countryFlag(detectCountryCode(name)); flag != "" {
		return flag + " " + name + " · CDN"
	}
	return name + " · CDN"
}
