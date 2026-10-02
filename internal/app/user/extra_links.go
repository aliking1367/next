package user

import (
	"strings"
)

// Extra links are configs an admin pastes onto a single user. They are not
// produced by any inbound, host or service: the point is to reach one user
// with something that works when everything the panel serves is blocked, so
// they are stored verbatim and appended to that user's subscription in every
// client format.

const (
	maxExtraLinksPerUser = 20
	maxExtraLinkLength   = 2048
)

// NormalizeExtraLinks cleans what an admin pasted: blank lines go, duplicates
// go, and anything that is not a share link is dropped rather than handed to
// a client that would choke on it. The admin's order is kept, because that is
// the order the links appear in the subscription.
func NormalizeExtraLinks(values []string) string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		// A single pasted block may hold several links.
		for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
			link := strings.TrimSpace(line)
			if link == "" || len(link) > maxExtraLinkLength || seen[link] {
				continue
			}
			if !isShareLink(link) {
				continue
			}
			seen[link] = true
			cleaned = append(cleaned, link)
			if len(cleaned) >= maxExtraLinksPerUser {
				return strings.Join(cleaned, "\n")
			}
		}
	}
	return strings.Join(cleaned, "\n")
}

// ParseExtraLinks reads the stored value back into the list the subscription
// appends.
func ParseExtraLinks(raw any) []string {
	text := strings.TrimSpace(stringValue(raw))
	if text == "" {
		return nil
	}
	var links []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if link := strings.TrimSpace(line); link != "" {
			links = append(links, link)
		}
	}
	return links
}

// isShareLink accepts the config URI schemes a client can import. Anything
// else -- a bare domain, a note the admin typed, a web page link -- would show up
// in the subscription as a broken entry, so it never gets stored.
func isShareLink(link string) bool {
	scheme, rest, found := strings.Cut(link, "://")
	if !found || strings.TrimSpace(rest) == "" {
		return false
	}
	// A real share link holds none of these. They are rejected here as well as
	// escaped where the link is written out, because a stored link reaches a
	// user's subscription page, a client's config list and anywhere else it is
	// displayed, and only one of those is under this code's control.
	if strings.ContainsAny(link, "<>\"") {
		return false
	}
	for _, r := range link {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "vless", "vmess", "trojan", "ss", "ssr", "hysteria", "hysteria2", "hy2",
		"tuic", "wireguard", "wg", "socks", "socks5", "anytls", "mieru":
		return true
	default:
		return false
	}
}
