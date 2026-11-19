package xrayconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// NordVPN auto configs. Every NordVPN location added as an outbound on the
// chosen target (the dialog names them nord-<hostname>) gets a config of its
// own: a VLESS + REALITY (Vision) inbound on that target, reached through the
// entry address (the Hetzner front), whose traffic leaves through that
// location. AI sites on these configs leave through one chosen location.
// Removing the outbound removes its config. This is separate from T.Sin: its
// inbounds never start with the T.Sin prefix, so applying the countries
// leaves them alone, and its rules only match its own inbounds.

// NordOutboundPrefix starts the tag of a NordVPN outbound.
const NordOutboundPrefix = "nord-"

// NordAutoInboundPrefix starts the tag of an inbound made for a location.
const NordAutoInboundPrefix = "nord-in-"

// NordAutoRulePrefix starts the ruleTag of the rules kept here.
const NordAutoRulePrefix = "nord-auto-"

// NordAutoAIRuleTag is the rule that sends AI sites to the AI location.
const NordAutoAIRuleTag = NordAutoRulePrefix + "ai"

// NordAutoAIDomains are the AI sites sent to the AI location.
var NordAutoAIDomains = []string{
	"openai.com", "chatgpt.com", "oaistatic.com", "oaiusercontent.com",
	"anthropic.com", "claude.ai", "claude.com",
	"gemini.google.com", "aistudio.google.com", "generativelanguage.googleapis.com",
	"perplexity.ai", "grok.com", "x.ai", "copilot.microsoft.com",
}

// NordAutoOptions is what a sync works from.
type NordAutoOptions struct {
	Target     string
	Address    string // the entry address the configs connect to
	SNI        string // the site REALITY borrows
	AIOutbound string // the location AI sites leave through; "" for none
	// Managed maps a location's outbound tag to its inbound tag.
	Managed  map[string]string
	PortBusy func(int) bool
	// FrontPort, when set (443), puts every location on that one port of the
	// entry address: a front inbound on the target reads each connection's SNI
	// and hands it to that location's config, which listens only on loopback.
	// Every location gets its own SNI from NordFrontSNIs (SNI first).
	FrontPort int
}

// NordFrontInboundTag is the front inbound that shares FrontPort.
const NordFrontInboundTag = "nord-front"

// NordFrontOutboundPrefix starts the outbounds that hand a front connection to
// a location's config; NordFrontFallbackTag takes the ones with an unknown SNI
// to the real site, as a plain web server would answer.
const (
	NordFrontOutboundPrefix = "nord-front-"
	NordFrontFallbackTag    = NordFrontOutboundPrefix + "fallback"
	nordFrontRuleTag        = NordAutoRulePrefix + "front"
)

// NordFrontSNIs are the sites the locations on the shared port borrow, one
// each: the entry server's own company, so the SNI fits the address.
var NordFrontSNIs = []string{
	"www.hetzner.com", "docs.hetzner.com", "accounts.hetzner.com", "console.hetzner.cloud",
	"www.hetzner.cloud", "status.hetzner.com", "community.hetzner.com", "robot.hetzner.com",
	"konsoleh.hetzner.com", "api.hetzner.cloud", "dns.hetzner.com", "hetzner.com",
}

// NordAutoLocation is one location's config.
type NordAutoLocation struct {
	Outbound string `json:"outbound"`
	Inbound  string `json:"inbound"`
	Port     int    `json:"port"`
	Code     string `json:"code"`
	Remark   string `json:"remark"`
	Created  bool   `json:"created"`
}

// NordAutoResult is what a sync did.
type NordAutoResult struct {
	Locations []NordAutoLocation `json:"locations"`
	Managed   map[string]string  `json:"-"`
	Created   []string           `json:"created"` // inbound tags made now
	Removed   []string           `json:"removed"` // inbound tags removed now
	Changed   bool               `json:"changed"`
}

// NordLocationCode is the country code in a location's hostname: "tr" for
// nord-tr80.nordvpn.com. NordVPN writes the UK as "uk".
func NordLocationCode(outboundTag string) string {
	name := strings.TrimPrefix(strings.ToLower(outboundTag), NordOutboundPrefix)
	end := 0
	for end < len(name) && name[end] >= 'a' && name[end] <= 'z' {
		end++
	}
	return name[:end]
}

// NordLocationRemark is the config's name: the country's flag and name.
func NordLocationRemark(code string) string {
	upper := strings.ToUpper(code)
	flagCode := upper
	if flagCode == "UK" {
		flagCode = "GB"
	}
	name, ok := nordCountryNames[upper]
	if !ok {
		return upper
	}
	return strings.TrimSpace(TSinFlag(flagCode) + " " + name)
}

var nordCountryNames = map[string]string{
	"AE": "UAE", "AL": "Albania", "AR": "Argentina", "AT": "Austria", "AU": "Australia",
	"BA": "Bosnia", "BE": "Belgium", "BG": "Bulgaria", "BR": "Brazil", "CA": "Canada",
	"CH": "Switzerland", "CL": "Chile", "CO": "Colombia", "CR": "Costa Rica", "CY": "Cyprus",
	"CZ": "Czechia", "DE": "Germany", "DK": "Denmark", "EE": "Estonia", "EG": "Egypt",
	"ES": "Spain", "FI": "Finland", "FR": "France", "GE": "Georgia", "GR": "Greece",
	"HK": "Hong Kong", "HR": "Croatia", "HU": "Hungary", "ID": "Indonesia", "IE": "Ireland",
	"IL": "Israel", "IN": "India", "IS": "Iceland", "IT": "Italy", "JP": "Japan",
	"KR": "South Korea", "KZ": "Kazakhstan", "LT": "Lithuania", "LU": "Luxembourg", "LV": "Latvia",
	"MD": "Moldova", "MK": "North Macedonia", "MX": "Mexico", "MY": "Malaysia", "NL": "Netherlands",
	"NO": "Norway", "NZ": "New Zealand", "PH": "Philippines", "PL": "Poland", "PT": "Portugal",
	"RO": "Romania", "RS": "Serbia", "SA": "Saudi Arabia", "SE": "Sweden", "SG": "Singapore",
	"SI": "Slovenia", "SK": "Slovakia", "TH": "Thailand", "TR": "Turkey", "TW": "Taiwan",
	"UA": "Ukraine", "UK": "UK", "US": "USA", "VN": "Vietnam", "ZA": "South Africa",
	"QA": "Qatar", "BH": "Bahrain", "KW": "Kuwait", "OM": "Oman", "AZ": "Azerbaijan",
	"AM": "Armenia", "IQ": "Iraq", "JO": "Jordan", "LB": "Lebanon", "PK": "Pakistan",
	"BD": "Bangladesh", "LK": "Sri Lanka", "NP": "Nepal", "KH": "Cambodia", "MN": "Mongolia",
	"UZ": "Uzbekistan", "KG": "Kyrgyzstan", "TN": "Tunisia", "MA": "Morocco", "DZ": "Algeria",
	"NG": "Nigeria", "KE": "Kenya", "GH": "Ghana", "AO": "Angola", "MZ": "Mozambique",
	"PE": "Peru", "VE": "Venezuela", "EC": "Ecuador", "UY": "Uruguay", "PY": "Paraguay",
	"BO": "Bolivia", "PA": "Panama", "GT": "Guatemala", "HN": "Honduras", "DO": "Dominican Republic",
	"JM": "Jamaica", "TT": "Trinidad and Tobago", "BS": "Bahamas", "PR": "Puerto Rico", "MT": "Malta",
	"ME": "Montenegro", "AD": "Andorra", "MC": "Monaco", "LI": "Liechtenstein", "BY": "Belarus",
	"BN": "Brunei", "LA": "Laos", "MM": "Myanmar", "PG": "Papua New Guinea", "GL": "Greenland",
	"BZ": "Belize", "BM": "Bermuda", "KY": "Cayman Islands", "IM": "Isle of Man", "JE": "Jersey",
	"SN": "Senegal", "SD": "Sudan", "LY": "Libya", "SY": "Syria", "YE": "Yemen", "AF": "Afghanistan",
}

// nordOutbounds are the target's NordVPN locations, in config order.
func nordOutbounds(config map[string]any) []string {
	tags := []string{}
	for _, item := range interfaceSlice(config["outbounds"]) {
		outbound := mapValue(item)
		tag := stringValue(outbound["tag"])
		if strings.HasPrefix(tag, NordOutboundPrefix) && strings.EqualFold(stringValue(outbound["protocol"]), "wireguard") {
			tags = append(tags, tag)
		}
	}
	return tags
}

// nordRuleOnlyInbounds reports the single inbound of a rule that matches
// nothing but that inbound, or "".
func nordRuleOnlyInbound(rule map[string]any) string {
	inbounds := interfaceSlice(rule["inboundTag"])
	if len(inbounds) != 1 {
		return ""
	}
	for key := range rule {
		switch key {
		case "type", "inboundTag", "outboundTag", "ruleTag":
		default:
			return ""
		}
	}
	return stringValue(inbounds[0])
}

// SyncNordAuto makes the target's configs follow its NordVPN outbounds: a new
// location gets an inbound, a host and a rule; a removed one loses them. An
// inbound already routed to a location by a rule of its own is adopted as that
// location's config rather than duplicated. It saves the target's routing only
// when it changes, so syncing with nothing new costs no node sync.
func (r Repository) SyncNordAuto(ctx context.Context, opts NordAutoOptions) (NordAutoResult, error) {
	result := NordAutoResult{Managed: map[string]string{}}
	target, err := normalizeTargetID(firstNonEmptyString(opts.Target, MasterTargetID))
	if err != nil {
		return result, err
	}
	address := strings.TrimSpace(opts.Address)
	if address == "" {
		return result, errors.New("set the address the configs connect to (the entry server)")
	}
	sni, err := NormalizeTSinVisionSNI(opts.SNI)
	if err != nil {
		return result, err
	}
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return result, err
	}
	outbounds := nordOutbounds(config)
	wanted := map[string]bool{}
	for _, tag := range outbounds {
		wanted[tag] = true
	}
	inbounds := map[string]bool{}
	for _, item := range interfaceSlice(config["inbounds"]) {
		inbounds[stringValue(mapValue(item)["tag"])] = true
	}
	managed := map[string]string{}
	for outbound, inbound := range opts.Managed {
		if inbounds[inbound] {
			managed[outbound] = inbound
		}
	}
	// Adopt an inbound already routed to a location by a rule of its own.
	claimed := map[string]bool{}
	for _, inbound := range managed {
		claimed[inbound] = true
	}
	for _, item := range interfaceSlice(mapValue(config["routing"])["rules"]) {
		rule := mapValue(item)
		outbound := stringValue(rule["outboundTag"])
		inbound := nordRuleOnlyInbound(rule)
		if inbound == "" || !wanted[outbound] || managed[outbound] != "" || claimed[inbound] || !inbounds[inbound] || strings.HasPrefix(inbound, TSinInboundPrefix) {
			continue
		}
		managed[outbound] = inbound
		claimed[inbound] = true
	}

	// Locations gone: their configs go.
	stale := make([]string, 0)
	for outbound := range managed {
		if !wanted[outbound] {
			stale = append(stale, outbound)
		}
	}
	sort.Strings(stale)
	for _, outbound := range stale {
		inbound := managed[outbound]
		if _, err := r.DeleteInbound(ctx, inbound); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return result, fmt.Errorf("remove %s: %w", inbound, err)
		}
		delete(managed, outbound)
		result.Removed = append(result.Removed, inbound)
	}

	// New locations: an inbound each, on a free port.
	frontPort := opts.FrontPort
	if frontPort < 0 || frontPort > 65535 {
		return result, errors.New("the shared port must be between 1 and 65535")
	}
	used, ranges := extractUsedPorts(config)
	if frontPort > 0 {
		used[frontPort] = true
	}
	frontSNIs := map[string]string{} // inbound tag -> its SNI on the shared port
	takenSNI := map[string]bool{}
	if frontPort > 0 {
		for _, inbound := range managed {
			if name := strings.ToLower(tsinVisionInboundSNI(findInbound(config, inbound))); name != "" {
				takenSNI[name] = true
			}
		}
	}
	for _, outbound := range outbounds {
		if managed[outbound] != "" {
			continue
		}
		tag := nordAutoInboundTag(outbound, inbounds)
		port, err := pickProvisionPort(nil, used, ranges, opts.PortBusy)
		if err != nil {
			return result, err
		}
		used[port] = true
		locationSNI := sni
		if frontPort > 0 {
			locationSNI = nextNordFrontSNI(sni, takenSNI)
			if locationSNI == "" {
				return result, errors.New("every SNI for the shared port is in use; remove a location first")
			}
			takenSNI[locationSNI] = true
		}
		payload, err := buildTSinVisionInbound(tag, port, locationSNI)
		if err != nil {
			return result, err
		}
		if frontPort > 0 {
			payload["listen"] = "127.0.0.1"
			stream := mapValue(payload["streamSettings"])
			stream["sockopt"] = map[string]any{"acceptProxyProtocol": true}
			frontSNIs[tag] = locationSNI
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return result, fmt.Errorf("%s: %w", outbound, err)
		}
		inbounds[tag] = true
		managed[outbound] = tag
		result.Created = append(result.Created, tag)
	}

	// Names, numbered when a country has more than one location.
	seen := map[string]int{}
	created := map[string]bool{}
	for _, tag := range result.Created {
		created[tag] = true
	}
	for _, outbound := range outbounds {
		code := NordLocationCode(outbound)
		seen[code]++
		remark := NordLocationRemark(code)
		if seen[code] > 1 {
			remark = fmt.Sprintf("%s %d", remark, seen[code])
		}
		location := NordAutoLocation{Outbound: outbound, Inbound: managed[outbound], Code: code, Remark: remark, Created: created[managed[outbound]]}
		if location.Created {
			// Only a config made now gets its host set; an adopted one keeps
			// whatever the admin made of it.
			var hostPort, hostSNI any
			if name := frontSNIs[location.Inbound]; name != "" {
				hostPort, hostSNI = frontPort, name
			}
			if _, err := r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = ?, sni = ?, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'chrome' WHERE inbound_tag = ?`, remark, address, hostPort, hostSNI, location.Inbound); err != nil {
				return result, err
			}
		}
		result.Locations = append(result.Locations, location)
	}

	// Routing: the AI rule first, then one rule per location.
	config, err = r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return result, err
	}
	for i := range result.Locations {
		result.Locations[i].Port = inboundPort(config, result.Locations[i].Inbound)
	}
	ours := map[string]bool{}
	for _, inbound := range managed {
		ours[inbound] = true
	}
	routing := mapValue(config["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	before, _ := json.Marshal([]any{routing["rules"], config["inbounds"], config["outbounds"]})
	kept := []any{}
	for _, item := range interfaceSlice(routing["rules"]) {
		rule := mapValue(item)
		if nordAutoOwnsRule(rule, ours) {
			continue
		}
		kept = append(kept, item)
	}
	if len(managed) > 0 {
		aiOutbound := strings.TrimSpace(opts.AIOutbound)
		if aiOutbound != "" && wanted[aiOutbound] {
			domains := make([]any, 0, len(NordAutoAIDomains))
			for _, domain := range NordAutoAIDomains {
				domains = append(domains, "domain:"+domain)
			}
			kept = append(kept, map[string]any{
				"type": "field", "ruleTag": NordAutoAIRuleTag,
				"inboundTag": stringsToAny(sortedValues(managed, outbounds)),
				"domain":     domains, "outboundTag": aiOutbound,
			})
		}
		for _, location := range result.Locations {
			kept = append(kept, map[string]any{
				"type": "field", "ruleTag": NordAutoRulePrefix + location.Inbound,
				"inboundTag": []any{location.Inbound}, "outboundTag": location.Outbound,
			})
		}
	}
	// The shared port: its front inbound, a hand-over outbound per location on
	// loopback and its rules, first so no block rule sees the loopback address.
	kept = applyNordFront(config, kept, frontPort, sni, result.Locations)
	after, _ := json.Marshal([]any{kept, config["inbounds"], config["outbounds"]})
	if string(before) != string(after) {
		routing["rules"] = kept
		config["routing"] = routing
		if _, err := r.SaveTargetRawConfig(ctx, target, config); err != nil {
			return result, err
		}
		result.Changed = true
	}
	if len(result.Created) > 0 || len(result.Removed) > 0 {
		result.Changed = true
	}
	result.Managed = managed
	return result, nil
}

// nextNordFrontSNI is the first site for the shared port not yet in use,
// starting with the chosen SNI.
func nextNordFrontSNI(first string, taken map[string]bool) string {
	for _, name := range append([]string{first}, NordFrontSNIs...) {
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "" && !taken[name] {
			return name
		}
	}
	return ""
}

func findInbound(config map[string]any, tag string) map[string]any {
	for _, item := range interfaceSlice(config["inbounds"]) {
		if inbound := mapValue(item); stringValue(inbound["tag"]) == tag {
			return inbound
		}
	}
	return nil
}

// applyNordFront rebuilds the shared port's pieces in config and returns the
// rules with the front's own ones first. Only locations whose config listens
// on loopback (made for the shared port) are put behind it; with no shared
// port, or none of those, the pieces are removed.
func applyNordFront(config map[string]any, rules []any, frontPort int, fallbackSNI string, locations []NordAutoLocation) []any {
	type entry struct {
		sni  string
		port int
		tag  string
	}
	entries := []entry{}
	if frontPort > 0 {
		for _, location := range locations {
			inbound := findInbound(config, location.Inbound)
			if inbound == nil || stringValue(inbound["listen"]) != "127.0.0.1" {
				continue
			}
			name := tsinVisionInboundSNI(inbound)
			port, _ := parseConfigPort(inbound["port"])
			if name == "" || port <= 0 {
				continue
			}
			entries = append(entries, entry{sni: strings.ToLower(name), port: port, tag: location.Inbound})
		}
	}

	inbounds := []any{}
	hadFront := false
	for _, item := range interfaceSlice(config["inbounds"]) {
		if stringValue(mapValue(item)["tag"]) == NordFrontInboundTag {
			hadFront = true
			continue
		}
		inbounds = append(inbounds, item)
	}
	outbounds := []any{}
	for _, item := range interfaceSlice(config["outbounds"]) {
		if strings.HasPrefix(stringValue(mapValue(item)["tag"]), NordFrontOutboundPrefix) {
			hadFront = true
			continue
		}
		outbounds = append(outbounds, item)
	}
	if len(entries) == 0 && !hadFront {
		return rules
	}
	front := []any{}
	if len(entries) > 0 {
		inbounds = append(inbounds, map[string]any{
			"tag": NordFrontInboundTag, "listen": "0.0.0.0", "port": frontPort, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": fallbackSNI, "port": 443, "network": "tcp"},
			"sniffing": map[string]any{"enabled": true, "destOverride": []any{"tls"}, "routeOnly": true},
		})
		for _, item := range entries {
			outbounds = append(outbounds, map[string]any{
				"tag": NordFrontOutboundPrefix + item.tag, "protocol": "freedom",
				"settings": map[string]any{"redirect": fmt.Sprintf("127.0.0.1:%d", item.port), "proxyProtocol": 2},
			})
			front = append(front, map[string]any{
				"type": "field", "ruleTag": nordFrontRuleTag + "-" + item.tag,
				"inboundTag": []any{NordFrontInboundTag}, "domain": []any{"full:" + item.sni},
				"outboundTag": NordFrontOutboundPrefix + item.tag,
			})
		}
		outbounds = append(outbounds, map[string]any{"tag": NordFrontFallbackTag, "protocol": "freedom", "settings": map[string]any{}})
		front = append(front, map[string]any{
			"type": "field", "ruleTag": nordFrontRuleTag,
			"inboundTag": []any{NordFrontInboundTag}, "outboundTag": NordFrontFallbackTag,
		})
	}
	config["inbounds"] = inbounds
	config["outbounds"] = outbounds
	return append(front, rules...)
}

// nordAutoOwnsRule reports a rule this sync keeps: one tagged as ours, or an
// untagged one that only routes our inbounds to a NordVPN location (made by
// hand before the automation, now taken over).
func nordAutoOwnsRule(rule map[string]any, ours map[string]bool) bool {
	if strings.HasPrefix(stringValue(rule["ruleTag"]), NordAutoRulePrefix) {
		return true
	}
	if !strings.HasPrefix(stringValue(rule["outboundTag"]), NordOutboundPrefix) {
		return false
	}
	inbounds := interfaceSlice(rule["inboundTag"])
	if len(inbounds) == 0 {
		return false
	}
	for _, inbound := range inbounds {
		if !ours[stringValue(inbound)] {
			return false
		}
	}
	return true
}

func nordAutoInboundTag(outbound string, taken map[string]bool) string {
	name := strings.TrimPrefix(strings.ToLower(outbound), NordOutboundPrefix)
	if dot := strings.IndexByte(name, '.'); dot > 0 {
		name = name[:dot]
	}
	base := NordAutoInboundPrefix + name
	tag := base
	for n := 2; taken[tag]; n++ {
		tag = fmt.Sprintf("%s-%d", base, n)
	}
	return tag
}

func inboundPort(config map[string]any, tag string) int {
	for _, item := range interfaceSlice(config["inbounds"]) {
		inbound := mapValue(item)
		if stringValue(inbound["tag"]) == tag {
			port, _ := parseConfigPort(inbound["port"])
			return port
		}
	}
	return 0
}

// sortedValues lists the inbounds in the order of their locations.
func sortedValues(managed map[string]string, order []string) []string {
	out := make([]string, 0, len(managed))
	for _, outbound := range order {
		if inbound := managed[outbound]; inbound != "" {
			out = append(out, inbound)
		}
	}
	return out
}
