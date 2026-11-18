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
	"AM": "Armenia",
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
	used, ranges := extractUsedPorts(config)
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
		payload, err := buildTSinVisionInbound(tag, port, sni)
		if err != nil {
			return result, err
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
			if _, err := r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = NULL, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'chrome' WHERE inbound_tag = ?`, remark, address, location.Inbound); err != nil {
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
	before, _ := json.Marshal(routing["rules"])
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
	after, _ := json.Marshal(kept)
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
