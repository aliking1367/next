// Package countryflag turns a node's name into the country flag users see in
// front of a config.
//
// It exists because the panel grew two of these independently: one labelling
// multi-location links, which understood Persian and Finglish spellings, and a
// later one labelling CDN host rows, which understood more cities and the bare
// two-letter codes. Each knew countries the other did not, so the flag a node
// got depended on which code path produced its label -- and a country added to
// one table silently did not reach the other. This is the one table.
package countryflag

import (
	"strings"
	"unicode"
)

// Country is one country and every spelling an admin might name a node with:
// English, Finglish, Persian, its major cities, and its two-letter code.
type Country struct {
	Code    string
	Aliases []string
}

// countries is searched in order, so a more specific name must come before a
// name it contains.
var countries = []Country{
	{"DE", []string{"germany", "deutschland", "alman", "almani", "آلمان", "frankfurt", "berlin", "munich", "nuremberg", "falkenstein", "de"}},
	{"NL", []string{"netherlands", "holland", "holand", "هلند", "amsterdam", "nl", "ams"}},
	{"US", []string{"united states", "usa", "america", "amrika", "آمریکا", "امریکا", "new york", "los angeles", "miami", "dallas", "chicago", "seattle", "ashburn", "atlanta", "us"}},
	{"GB", []string{"united kingdom", "england", "britain", "london", "manchester", "engelis", "انگلیس", "انگلستان", "بریتانیا", "uk", "gb", "lon"}},
	{"FR", []string{"france", "faranse", "فرانسه", "paris", "marseille", "gravelines", "fr", "par"}},
	{"FI", []string{"finland", "fanland", "فنلاند", "helsinki", "fi"}},
	{"SE", []string{"sweden", "soed", "سوئد", "stockholm", "se"}},
	{"NO", []string{"norway", "norvej", "نروژ", "oslo", "no"}},
	{"DK", []string{"denmark", "danmark", "دانمارک", "copenhagen", "dk"}},
	{"TR", []string{"turkey", "turkiye", "türkiye", "torkiye", "ترکیه", "istanbul", "استانبول", "ankara", "izmir", "tr", "ist"}},
	{"AE", []string{"emirates", "uae", "dubai", "abu dhabi", "emarat", "امارات", "دبی", "ae"}},
	{"CA", []string{"canada", "kanada", "کانادا", "toronto", "montreal", "vancouver", "ca"}},
	{"RU", []string{"russia", "rusiye", "روسیه", "moscow", "ru"}},
	{"JP", []string{"japan", "zhapon", "ژاپن", "tokyo", "osaka", "jp", "nrt"}},
	{"SG", []string{"singapore", "sangapur", "سنگاپور", "sg", "sin"}},
	{"PL", []string{"poland", "lahestan", "لهستان", "warsaw", "pl"}},
	{"AT", []string{"austria", "otrish", "اتریش", "vienna", "at"}},
	{"CH", []string{"switzerland", "swiss", "سوئیس", "zurich", "geneva", "ch"}},
	{"IT", []string{"italy", "italia", "ایتالیا", "milan", "rome", "it"}},
	{"ES", []string{"spain", "espania", "اسپانیا", "madrid", "barcelona", "es"}},
	{"PT", []string{"portugal", "پرتغال", "lisbon", "pt"}},
	{"IE", []string{"ireland", "ایرلند", "dublin", "ie"}},
	{"BE", []string{"belgium", "بلژیک", "brussels", "be"}},
	{"AM", []string{"armenia", "armanestan", "ارمنستان", "yerevan", "am"}},
	{"GE", []string{"georgia", "gorjestan", "گرجستان", "tbilisi", "ge"}},
	{"AZ", []string{"azerbaijan", "آذربایجان", "baku", "az"}},
	{"HU", []string{"hungary", "majarestan", "مجارستان", "budapest", "hu"}},
	{"RO", []string{"romania", "رومانی", "bucharest", "ro"}},
	{"BG", []string{"bulgaria", "بلغارستان", "sofia", "bg"}},
	{"CZ", []string{"czechia", "czech", "chek", "چک", "prague", "cz"}},
	{"LT", []string{"lithuania", "لیتوانی", "vilnius", "lt"}},
	{"LV", []string{"latvia", "لتونی", "riga", "lv"}},
	{"EE", []string{"estonia", "استونی", "tallinn", "ee"}},
	{"UA", []string{"ukraine", "اوکراین", "kyiv", "kiev", "ua"}},
	{"MD", []string{"moldova", "مولداوی", "chisinau", "md"}},
	{"RS", []string{"serbia", "صربستان", "belgrade", "rs"}},
	{"GR", []string{"greece", "یونان", "athens", "gr"}},
	{"IS", []string{"iceland", "ایسلند", "reykjavik"}},
	{"LU", []string{"luxembourg", "لوکزامبورگ", "lu"}},
	{"CY", []string{"cyprus", "قبرس", "cy"}},
	{"HK", []string{"hong kong", "hongkong", "هنگ کنگ", "hk"}},
	{"TW", []string{"taiwan", "تایوان", "taipei", "tw"}},
	{"KR", []string{"south korea", "korea", "کره", "seoul", "kr"}},
	{"CN", []string{"china", "چین", "shanghai", "beijing", "cn"}},
	{"IN", []string{"india", "hend", "هند", "mumbai", "delhi", "bangalore"}},
	{"ID", []string{"indonesia", "اندونزی", "jakarta", "id"}},
	{"MY", []string{"malaysia", "مالزی", "kuala lumpur", "my"}},
	{"TH", []string{"thailand", "تایلند", "bangkok", "th"}},
	{"VN", []string{"vietnam", "ویتنام", "hanoi", "saigon", "vn"}},
	{"PH", []string{"philippines", "فیلیپین", "manila", "ph"}},
	{"AU", []string{"australia", "استرالیا", "sydney", "melbourne", "au", "syd"}},
	{"NZ", []string{"new zealand", "نیوزیلند", "auckland", "nz"}},
	{"QA", []string{"qatar", "ghatar", "قطر", "doha", "qa"}},
	{"KW", []string{"kuwait", "کویت", "kw"}},
	{"BH", []string{"bahrain", "بحرین", "bh"}},
	{"OM", []string{"oman", "عمان", "muscat", "om"}},
	{"SA", []string{"saudi", "عربستان", "riyadh", "jeddah", "sa"}},
	{"IL", []string{"israel", "اسرائیل", "tel aviv", "il"}},
	{"KZ", []string{"kazakhstan", "قزاقستان", "almaty", "kz"}},
	{"BR", []string{"brazil", "برزیل", "sao paulo", "br"}},
	{"AR", []string{"argentina", "آرژانتین", "buenos aires", "ar"}},
	{"CL", []string{"chile", "شیلی", "santiago", "cl"}},
	{"MX", []string{"mexico", "مکزیک", "mx"}},
	{"ZA", []string{"south africa", "آفریقای جنوبی", "johannesburg", "cape town", "za"}},
	{"EG", []string{"egypt", "مصر", "cairo", "eg"}},
	{"NG", []string{"nigeria", "نیجریه", "lagos", "ng"}},
	{"KE", []string{"kenya", "کنیا", "nairobi", "ke"}},
	{"IR", []string{"iran", "ایران", "tehran", "تهران", "ir"}},
}

// Flag turns a two-letter ISO country code into its flag, which is the two
// matching regional indicator symbols.
func Flag(code string) string {
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

// Detect finds the country a node name refers to, or "" when nothing matches.
//
// Nothing is the right answer more often than a guess: a user choosing a server
// by country is misled by a wrong flag in a way they cannot detect, while a
// missing one only looks plain, and the admin can put the flag in the name.
func Detect(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return ""
	}
	// A bare code is only read as one when the name is nothing else. Loose,
	// "is", "at", "in", "no" and "my" are all country codes, and "node in
	// europe" must not come out Indian.
	bare := strings.Trim(lower, " -_.")
	for _, country := range countries {
		for _, alias := range country.Aliases {
			if len(alias) > 2 {
				continue
			}
			if bare == alias {
				return country.Code
			}
		}
	}
	for _, country := range countries {
		for _, alias := range country.Aliases {
			if len(alias) <= 2 {
				continue
			}
			if containsWord(lower, alias) {
				return country.Code
			}
		}
	}
	return ""
}

// HasLeadingFlag reports whether a name already begins with a flag, so one an
// admin wrote is kept rather than replaced or doubled.
func HasLeadingFlag(name string) bool {
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

// Label puts the flag in front of a name. A name that already carries one is
// returned unchanged, and an empty name falls back to whatever the caller has
// instead -- usually the address, so a nameless node is still identifiable.
func Label(name, fallback string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		return fallback
	}
	if HasLeadingFlag(label) {
		return label
	}
	if flag := Flag(Detect(label)); flag != "" {
		return flag + " " + label
	}
	return label
}

// containsWord matches an alias as a whole word, so "uk" never matches inside
// another word and "chek" does not match "checkout". Multi-word aliases such as
// "south korea" and "hong kong" match directly, which is why this works on the
// whole string rather than on split tokens -- splitting would also throw away
// Persian, since those names are not ASCII.
func containsWord(text, alias string) bool {
	for start := 0; start < len(text); {
		index := strings.Index(text[start:], alias)
		if index < 0 {
			return false
		}
		index += start
		if wordBoundary(text, index-1) && wordBoundary(text, index+len(alias)) {
			return true
		}
		start = index + 1
	}
	return false
}

func wordBoundary(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return true
	}
	r := rune(text[index])
	if r >= 0x80 {
		// Inside a multi-byte (for example Persian) word: decode the full rune
		// before deciding, or every byte of it would read as a boundary.
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
