package user

import (
	"context"
	"strings"
	"unicode"
)

// ConfigLocation is one node that serves the shared Xray config. Hosts whose
// address is {SERVER_IP} are offered once per location ("multi-location"),
// so a subscription lists every country the panel runs nodes in.
type ConfigLocation struct {
	Name    string
	Address string
}

// configLocations lists the nodes that run the shared config, in node order.
// Custom-config, disabled, limited and deleted nodes are left out. Any error
// (for example an older schema) yields no locations, which keeps the classic
// single-address links.
func (r Repository) configLocations(ctx context.Context) []ConfigLocation {
	rows, err := r.db.QueryContext(ctx, `SELECT COALESCE(name, ''), COALESCE(NULLIF(TRIM(COALESCE(public_address, '')), ''), address) FROM nodes
WHERE TRIM(COALESCE(address, '')) != ''
  AND LOWER(COALESCE(status, '')) NOT IN ('deleted', 'disabled', 'limited')
  AND LOWER(COALESCE(xray_config_mode, 'default')) <> 'custom'
ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var locations []ConfigLocation
	for rows.Next() {
		var name, address string
		if err := rows.Scan(&name, &address); err != nil {
			return nil
		}
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		locations = append(locations, ConfigLocation{Name: locationLabel(name, address), Address: address})
	}
	if rows.Err() != nil {
		return nil
	}
	return locations
}

// hostUsesServerIP reports whether a host points at the node address
// placeholder; only those hosts are repeated per location.
func hostUsesServerIP(host Host) bool {
	if strings.Contains(strings.ToUpper(host.Address), "{SERVER_IP}") {
		return true
	}
	for _, option := range host.AddressOptions {
		if strings.Contains(strings.ToUpper(option), "{SERVER_IP}") {
			return true
		}
	}
	return false
}

// hostRemarkNamesLocation reports whether the admin already placed the
// location in the remark; otherwise it is prefixed automatically.
func hostRemarkNamesLocation(host Host) bool {
	upper := strings.ToUpper(host.Remark)
	return strings.Contains(upper, "{LOCATION}") || strings.Contains(upper, "{NODE_NAME}")
}

// locationLabel turns a node name into what users see, adding the country
// flag when the name mentions a known country ("Germany", "Alman", "آلمان").
func locationLabel(name, address string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		return address
	}
	if startsWithFlag(label) {
		return label
	}
	if flag := countryFlagFor(label); flag != "" {
		return flag + " " + label
	}
	return label
}

func startsWithFlag(value string) bool {
	for _, r := range value {
		return r >= 0x1F1E6 && r <= 0x1F1FF
	}
	return false
}

var countryNames = []struct {
	code  string
	names []string
}{
	{"DE", []string{"germany", "deutschland", "alman", "آلمان", "frankfurt", "berlin", "nuremberg", "falkenstein"}},
	{"NL", []string{"netherlands", "holland", "holand", "هلند", "amsterdam"}},
	{"US", []string{"usa", "united states", "america", "amrika", "آمریکا", "امریکا", "new york", "los angeles", "miami", "dallas", "chicago", "seattle"}},
	{"GB", []string{"united kingdom", "england", "britain", "london", "engelis", "انگلیس", "انگلستان", "بریتانیا"}},
	{"FR", []string{"france", "faranse", "فرانسه", "paris"}},
	{"FI", []string{"finland", "fanland", "فنلاند", "helsinki"}},
	{"SE", []string{"sweden", "soed", "سوئد", "stockholm"}},
	{"TR", []string{"turkey", "turkiye", "türkiye", "torkiye", "ترکیه", "istanbul", "استانبول"}},
	{"AE", []string{"emirates", "uae", "dubai", "emarat", "امارات", "دبی"}},
	{"CA", []string{"canada", "kanada", "کانادا", "toronto"}},
	{"RU", []string{"russia", "rusiye", "روسیه", "moscow"}},
	{"JP", []string{"japan", "zhapon", "ژاپن", "tokyo"}},
	{"SG", []string{"singapore", "sangapur", "سنگاپور"}},
	{"PL", []string{"poland", "lahestan", "لهستان", "warsaw"}},
	{"AT", []string{"austria", "otrish", "اتریش", "vienna"}},
	{"CH", []string{"switzerland", "swiss", "سوئیس", "zurich"}},
	{"IT", []string{"italy", "italia", "ایتالیا", "milan"}},
	{"ES", []string{"spain", "espania", "اسپانیا", "madrid"}},
	{"AM", []string{"armenia", "armanestan", "ارمنستان", "yerevan"}},
	{"GE", []string{"georgia", "gorjestan", "گرجستان", "tbilisi"}},
	{"HU", []string{"hungary", "majarestan", "مجارستان", "budapest"}},
	{"RO", []string{"romania", "رومانی", "bucharest"}},
	{"LT", []string{"lithuania", "لیتوانی", "vilnius"}},
	{"EE", []string{"estonia", "استونی", "tallinn"}},
	{"HK", []string{"hong kong", "hongkong", "هنگ کنگ"}},
	{"IN", []string{"india", "hend", "هند", "mumbai"}},
	{"IR", []string{"iran", "ایران", "tehran", "تهران"}},
	{"QA", []string{"qatar", "ghatar", "قطر"}},
	{"KZ", []string{"kazakhstan", "قزاقستان"}},
	{"CZ", []string{"czech", "chek", "چک", "prague"}},
	{"BG", []string{"bulgaria", "بلغارستان", "sofia"}},
	{"NO", []string{"norway", "norvej", "نروژ", "oslo"}},
	{"DK", []string{"denmark", "danmark", "دانمارک", "copenhagen"}},
	{"AU", []string{"australia", "استرالیا", "sydney"}},
	{"BR", []string{"brazil", "برزیل"}},
	{"KR", []string{"korea", "کره", "seoul"}},
}

func countryFlagFor(name string) string {
	lower := strings.ToLower(name)
	for _, country := range countryNames {
		for _, candidate := range country.names {
			if containsWord(lower, candidate) {
				return flagEmoji(country.code)
			}
		}
	}
	return ""
}

// containsWord matches candidate as a whole word, so "uk" never matches
// inside another word and "chek" does not match "checkout".
func containsWord(text, candidate string) bool {
	for start := 0; ; {
		index := strings.Index(text[start:], candidate)
		if index < 0 {
			return false
		}
		index += start
		end := index + len(candidate)
		if wordBoundary(text, index-1) && wordBoundary(text, end) {
			return true
		}
		start = index + 1
		if start >= len(text) {
			return false
		}
	}
}

func wordBoundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	r := rune(text[index])
	if r >= 0x80 {
		// Inside a multi-byte (for example Persian) word: look at the full rune.
		for i := index; i >= 0; i-- {
			if text[i]&0xC0 != 0x80 {
				decoded := []rune(text[i:])
				if len(decoded) > 0 {
					return !unicode.IsLetter(decoded[0]) && !unicode.IsDigit(decoded[0])
				}
				break
			}
		}
		return true
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func flagEmoji(code string) string {
	code = strings.ToUpper(code)
	if len(code) != 2 {
		return ""
	}
	return string([]rune{0x1F1E6 + rune(code[0]-'A'), 0x1F1E6 + rune(code[1]-'A')})
}
