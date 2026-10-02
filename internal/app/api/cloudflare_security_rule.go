package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// A CDN-fronted config reaches Cloudflare as a proxy protocol on a secret path,
// not as a browser. When Cloudflare decides to challenge it, the client gets a
// plain 403 -- it cannot run the JavaScript a challenge asks for -- and the
// config reads as simply broken. One rule telling Cloudflare not to challenge
// these hostnames is what makes CDN fronting work at all, and it is the single
// step that otherwise has to be repeated by hand for every node added.
//
// Turning a security feature off deserves care, so the scope is narrow and
// enforced here rather than left to the caller:
//
//   - Only hostnames the panel generated and recorded in nodes.cdn_hostname are
//     eligible. They answer for a node, where the only service is the Xray
//     inbound -- no login form, no admin surface, no database. There is no web
//     application behind them for a web application firewall to protect.
//   - The panel's own hostname is excluded explicitly, even if a node happens to
//     be named such that its label collides with it. The panel has a dashboard
//     and an API and keeps every protection.
//   - Only Cloudflare's own checks are skipped, not the admin's rules. The
//     action does not pass ruleset "current", so a custom rule the admin wrote
//     still applies to these hostnames.
//   - Every other rule in the zone is read and written back unchanged, because
//     the custom-rules phase has one ruleset per zone and the admin's own rules
//     live in it.

// cdnSkipRuleDescription identifies the one rule the panel owns. Every other
// rule in the ruleset is the admin's and is preserved verbatim.
const cdnSkipRuleDescription = "next-panel-cdn-skip"

const customRulesPhase = "http_request_firewall_custom"

// Fields Cloudflare returns on a read but refuses on a write. Everything else is
// copied back as-is, so a rule the admin wrote survives exactly as they left it.
var rulesetReadOnlyFields = []string{"version", "last_updated"}

type cloudflareRuleset struct {
	ID    string            `json:"id"`
	Phase string            `json:"phase"`
	Kind  string            `json:"kind"`
	Rules []json.RawMessage `json:"rules"`
}

// cdnSkipRuleHostnames filters the hostnames that may appear in the rule.
// Anything that is not a well-formed public hostname under the managed zone, or
// that matches the panel's own hostname, is dropped.
func cdnSkipRuleHostnames(hostnames []string, suffix string, panelHostname string) []string {
	suffix = strings.ToLower(strings.TrimSpace(suffix))
	panelHostname = strings.ToLower(strings.TrimSpace(panelHostname))
	clean := make([]string, 0, len(hostnames))
	seen := map[string]bool{}
	for _, hostname := range hostnames {
		candidate := strings.ToLower(strings.TrimSpace(hostname))
		if candidate == "" || seen[candidate] {
			continue
		}
		if !isValidPublicHostnameLocal(candidate) {
			continue
		}
		// Must sit under the zone this feature manages, so a stray value cannot
		// widen the rule to some other name.
		if suffix != "" && !strings.HasSuffix(candidate, "."+suffix) {
			continue
		}
		// The panel keeps every protection, whatever a node is called.
		if panelHostname != "" && candidate == panelHostname {
			continue
		}
		seen[candidate] = true
		clean = append(clean, candidate)
	}
	return clean
}

// ensureCDNSkipRule writes the rule, creating the zone's custom-rules ruleset if
// it has none. It returns the number of hostnames covered.
func (c cloudflareClient) ensureCDNSkipRule(ctx context.Context, zoneID string, hostnames []string) (int, error) {
	if len(hostnames) == 0 {
		return 0, nil
	}
	var quoted strings.Builder
	for i, hostname := range hostnames {
		if i > 0 {
			quoted.WriteByte(' ')
		}
		quoted.WriteString(`"` + hostname + `"`)
	}
	ours := map[string]any{
		"action": "skip",
		"action_parameters": map[string]any{
			// Cloudflare's own checks only. "current" is deliberately absent:
			// skipping the rest of the custom rules would also bypass a rule the
			// admin wrote, which is not this feature's business.
			"phases": []string{
				"http_ratelimit",
				"http_request_firewall_managed",
				"http_request_sbfm",
			},
			"products": []string{
				"waf", "rateLimit", "securityLevel", "hot", "bic", "uaBlock", "zoneLockdown",
			},
		},
		"expression":  "(http.host in {" + quoted.String() + "})",
		"description": cdnSkipRuleDescription,
		"enabled":     true,
		"logging":     map[string]any{"enabled": true},
	}

	existing, err := c.customRuleset(ctx, zoneID)
	if err != nil {
		return 0, err
	}
	rules := []any{ours}
	replaced := false
	for _, raw := range existing.Rules {
		var rule map[string]any
		if err := json.Unmarshal(raw, &rule); err != nil {
			// Refusing to write beats writing a ruleset with one of the admin's
			// rules missing from it.
			return 0, cloudflareError{Message: "could not read an existing security rule, so nothing was changed"}
		}
		if description, _ := rule["description"].(string); description == cdnSkipRuleDescription {
			if !replaced {
				// Keeping the id updates our rule in place rather than leaving a
				// stale copy behind it.
				if id, ok := rule["id"].(string); ok && id != "" {
					ours["id"] = id
				}
				replaced = true
			}
			continue
		}
		for _, field := range rulesetReadOnlyFields {
			delete(rule, field)
		}
		rules = append(rules, rule)
	}

	payload := map[string]any{"rules": rules}
	if existing.ID != "" {
		if _, err := c.do(ctx, http.MethodPut, "/zones/"+zoneID+"/rulesets/"+existing.ID, payload); err != nil {
			return 0, err
		}
		return len(hostnames), nil
	}
	// The zone has no custom-rules ruleset yet; writing the phase entrypoint
	// creates it.
	if _, err := c.do(ctx, http.MethodPut,
		"/zones/"+zoneID+"/rulesets/phases/"+customRulesPhase+"/entrypoint", payload); err != nil {
		return 0, err
	}
	return len(hostnames), nil
}

// customRuleset returns the zone's custom-rules ruleset, or an empty one when
// the zone has none.
func (c cloudflareClient) customRuleset(ctx context.Context, zoneID string) (cloudflareRuleset, error) {
	result, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/rulesets", nil)
	if err != nil {
		return cloudflareRuleset{}, err
	}
	var rulesets []cloudflareRuleset
	if err := json.Unmarshal(result, &rulesets); err != nil {
		return cloudflareRuleset{}, cloudflareError{Message: "could not read the zone's rule list"}
	}
	for _, ruleset := range rulesets {
		if ruleset.Phase != customRulesPhase || ruleset.Kind == "managed" {
			continue
		}
		detail, err := c.do(ctx, http.MethodGet, "/zones/"+zoneID+"/rulesets/"+ruleset.ID, nil)
		if err != nil {
			return cloudflareRuleset{}, err
		}
		var full cloudflareRuleset
		if err := json.Unmarshal(detail, &full); err != nil {
			return cloudflareRuleset{}, cloudflareError{Message: "could not read the zone's security rules"}
		}
		if full.ID == "" {
			full.ID = ruleset.ID
		}
		return full, nil
	}
	return cloudflareRuleset{}, nil
}
