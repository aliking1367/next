package user

import (
	"encoding/json"
	"regexp"
	"strings"
)

// The smart config pairs the direct (⚡) Best ping config with its Cloudflare
// one in a single Xray JSON profile at the top of the subscription: traffic
// goes the direct way while it answers and falls back to Cloudflare on its
// own when it stops, so a user whose direct address gets filtered stays
// connected without picking another config. Best ping because every country
// shares the same way in (front server, then this server); only the last leg
// differs, and T.Sin's Best ping already picks it from the server side.
//
// It is added when the owner turns it on (tsin_settings.smart_config) or
// when the subscription is asked for with ?smart=1. ?smart=fail breaks the
// direct way and ?smart=nocdn the Cloudflare one, to see each of them carry
// the traffic alone. Every other subscription renders exactly as before.

// smartDirectRemark is the suffix the panel gives a country's direct config
// (xrayconfig.tsinDirectRemark).
const smartDirectRemark = " ⚡"

const (
	smartDirectTag   = "proxy"
	smartFallbackTag = "cdn"
	smartBalancerTag = "smart"
	// smartBrokenAddress is a documentation-only address (RFC 5737) nothing
	// answers on, for the test profiles.
	smartBrokenAddress = "192.0.2.1"

	smartModeOn    = "on"
	smartModeFail  = "fail"
	smartModeNoCDN = "nocdn"

	// SmartConfigOff, SmartConfigJSON and SmartConfigV2Box are the owner's
	// switch: off, in the JSON subscriptions apps already get, and also
	// sending V2Box the JSON subscription.
	SmartConfigOff   = 0
	SmartConfigJSON  = 1
	SmartConfigV2Box = 2
)

var smartV2BoxAgent = regexp.MustCompile(`(?i)^v2box`)

// NormalizeSmartConfig keeps the switch to a known value.
func NormalizeSmartConfig(level int) int {
	if level == SmartConfigJSON || level == SmartConfigV2Box {
		return level
	}
	return SmartConfigOff
}

// smartMode picks the smart mode of one request: the ?smart= value when it
// names one, otherwise on when the owner's switch is.
func smartMode(query string, level int) string {
	switch strings.ToLower(strings.TrimSpace(query)) {
	case "1", "true", "on", "yes":
		return smartModeOn
	case "fail":
		return smartModeFail
	case "nocdn":
		return smartModeNoCDN
	}
	if NormalizeSmartConfig(level) != SmartConfigOff {
		return smartModeOn
	}
	return ""
}

// smartClientType sends V2Box the JSON subscription when the owner chose so,
// where it would otherwise get plain links. Other apps, and a V2Box an admin
// routed to any other format, keep what they get. RenderSubscription falls
// back to the plain links when a user's configs cannot become JSON.
func smartClientType(clientType, userAgent string, level int) string {
	if clientType == "v2ray" && NormalizeSmartConfig(level) == SmartConfigV2Box && smartV2BoxAgent.MatchString(strings.TrimSpace(userAgent)) {
		return "v2ray-json"
	}
	return clientType
}

// addSmartConfigs puts the smart profile at the top of an Xray JSON
// subscription, built from the Best ping pair (or the first country with both
// a direct and a Cloudflare config). Anything it cannot read is left as it
// was: the subscription itself never fails here.
func addSmartConfigs(body string, mode string) string {
	if mode == "" {
		return body
	}
	var configs []map[string]any
	if err := json.Unmarshal([]byte(body), &configs); err != nil {
		return body
	}
	byRemark := make(map[string]map[string]any, len(configs))
	for _, config := range configs {
		if remark := stringValue(config["remarks"]); remark != "" {
			if _, seen := byRemark[remark]; !seen {
				byRemark[remark] = config
			}
		}
	}
	var direct, fallback map[string]any
	var base string
	for _, config := range configs {
		remark := stringValue(config["remarks"])
		if !strings.HasSuffix(remark, smartDirectRemark) {
			continue
		}
		name := strings.TrimSuffix(remark, smartDirectRemark)
		pair, ok := byRemark[name]
		if !ok {
			continue
		}
		best := strings.Contains(strings.ToLower(name), "best")
		if direct == nil || best {
			direct, fallback, base = config, pair, name
		}
		if best {
			break
		}
	}
	if direct == nil {
		return body
	}
	profile, ok := smartProfile(direct, fallback, base, mode)
	if !ok {
		return body
	}
	out, err := marshalPretty(append([]map[string]any{profile}, configs...))
	if err != nil {
		return body
	}
	return out
}

// smartRemark names the smart profile.
func smartRemark(base, mode string) string {
	switch mode {
	case smartModeFail:
		return "⭐ تست هوشمند: ⚡ خاموش"
	case smartModeNoCDN:
		return "⭐ تست هوشمند: Cloudflare خاموش"
	}
	if strings.Contains(strings.ToLower(base), "best") {
		return "⭐ هوشمند (بهترین لوکیشن)"
	}
	return "⭐ هوشمند (" + base + ")"
}

// smartProfile builds the smart profile from a direct config and the
// Cloudflare config of the same country.
func smartProfile(direct, fallback map[string]any, base, mode string) (map[string]any, bool) {
	directOut, directRest, ok := splitProxyOutbound(direct)
	if !ok {
		return nil, false
	}
	fallbackOut, _, ok := splitProxyOutbound(fallback)
	if !ok {
		return nil, false
	}
	directOut = cloneJSONMap(directOut)
	fallbackOut = cloneJSONMap(fallbackOut)
	directOut["tag"] = smartDirectTag
	fallbackOut["tag"] = smartFallbackTag
	remark := smartRemark(base, mode)
	switch mode {
	case smartModeFail:
		breakOutbound(directOut)
	case smartModeNoCDN:
		breakOutbound(fallbackOut)
	}

	profile := cloneJSONMap(direct)
	profile["remarks"] = remark
	outbounds := []any{directOut, fallbackOut}
	for _, outbound := range directRest {
		tag := stringValue(mapValue(outbound)["tag"])
		// The balancer picks by tag prefix; keep the template's own
		// outbounds (direct, block, ...) from being mistaken for ours.
		if strings.HasPrefix(tag, smartDirectTag) || strings.HasPrefix(tag, smartFallbackTag) {
			continue
		}
		outbounds = append(outbounds, outbound)
	}
	profile["outbounds"] = outbounds

	// The observatory checks the direct way through itself every 15 seconds.
	// While it answers, leastPing picks it; when it does not, nothing is
	// alive in the selector and the balancer uses the fallback. Before the
	// first check finishes, the first seconds go over Cloudflare.
	profile["observatory"] = map[string]any{
		"subjectSelector":   []any{smartDirectTag},
		"probeUrl":          "https://www.gstatic.com/generate_204",
		"probeInterval":     "15s",
		"enableConcurrency": true,
	}
	routing := cloneJSONMap(mapValue(profile["routing"]))
	if routing == nil {
		routing = map[string]any{}
	}
	routing["balancers"] = []any{map[string]any{
		"tag":         smartBalancerTag,
		"selector":    []any{smartDirectTag},
		"fallbackTag": smartFallbackTag,
		"strategy":    map[string]any{"type": "leastPing"},
	}}
	rules := make([]any, 0)
	for _, rule := range listAny(routing["rules"]) {
		// A template rule that names the proxy outbound now means the
		// balancer, so the fallback applies to it too.
		if ruleMap := mapValue(rule); ruleMap != nil && stringValue(ruleMap["outboundTag"]) == smartDirectTag {
			ruleMap = cloneJSONMap(ruleMap)
			delete(ruleMap, "outboundTag")
			ruleMap["balancerTag"] = smartBalancerTag
			rules = append(rules, ruleMap)
			continue
		}
		rules = append(rules, rule)
	}
	rules = append(rules, map[string]any{
		"type":        "field",
		"network":     "tcp,udp",
		"balancerTag": smartBalancerTag,
	})
	routing["rules"] = rules
	profile["routing"] = routing
	return profile, true
}

// splitProxyOutbound returns a config's proxy outbound (the one tagged
// "proxy", or the first) and its other outbounds.
func splitProxyOutbound(config map[string]any) (map[string]any, []any, bool) {
	outbounds := listAny(config["outbounds"])
	index := -1
	for i, outbound := range outbounds {
		if stringValue(mapValue(outbound)["tag"]) == smartDirectTag {
			index = i
			break
		}
	}
	if index < 0 && len(outbounds) > 0 {
		index = 0
	}
	if index < 0 {
		return nil, nil, false
	}
	proxy := mapValue(outbounds[index])
	if proxy == nil {
		return nil, nil, false
	}
	rest := make([]any, 0, len(outbounds)-1)
	rest = append(rest, outbounds[:index]...)
	rest = append(rest, outbounds[index+1:]...)
	return proxy, rest, true
}

// breakOutbound points an outbound at an address nothing answers on.
func breakOutbound(outbound map[string]any) {
	for _, server := range listOfMaps(mapValue(outbound["settings"])["vnext"]) {
		server["address"] = smartBrokenAddress
	}
}
