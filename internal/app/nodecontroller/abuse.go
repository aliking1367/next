package nodecontroller

import (
	"context"
	"strings"
)

// Abuse protection blocks the traffic that brings abuse reports to the
// servers users go out from: BitTorrent, which draws copyright complaints,
// and outgoing mail on port 25, which spam goes through. The rules are added
// to the config each Xray gets, in front of every other rule, so traffic to a
// T.Sin country is covered too; the stored configs are not changed. Mail apps
// keep working: they send on 465 and 587, which stay open.

const abuseBlockTag = "abuse-block"

// abuseSniffedProtocols are the inbound protocols whose traffic can be
// recognised as BitTorrent; Xray needs sniffing on them to see it.
var abuseSniffedProtocols = map[string]bool{
	"vless": true, "vmess": true, "trojan": true, "shadowsocks": true, "socks": true, "http": true,
}

// AbuseProtection reports whether the switch is on. It is on unless an admin
// turned it off, including when the setting cannot be read.
func (r Repository) AbuseProtection(ctx context.Context) bool {
	var enabled bool
	if err := r.db.QueryRowContext(ctx, `SELECT COALESCE(abuse_protection, 1) FROM settings WHERE id = 1`).Scan(&enabled); err != nil {
		return true
	}
	return enabled
}

// withAbuseProtection adds the blocking rules to a runtime config. A config
// without outbounds is left alone: there the blackhole would become the
// default way out.
func withAbuseProtection(raw map[string]any) map[string]any {
	outbounds := interfaceSlice(raw["outbounds"])
	if len(outbounds) == 0 {
		return raw
	}
	hasBlock := false
	for _, item := range outbounds {
		if outbound, ok := item.(map[string]any); ok && stringValue(outbound["tag"]) == abuseBlockTag {
			hasBlock = true
		}
	}
	if !hasBlock {
		raw["outbounds"] = append(outbounds, map[string]any{"protocol": "blackhole", "tag": abuseBlockTag})
	}

	routing := mapValue(raw["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	rules := interfaceSlice(routing["rules"])
	kept := make([]any, 0, len(rules)+2)
	kept = append(kept,
		map[string]any{"type": "field", "protocol": []any{"bittorrent"}, "outboundTag": abuseBlockTag},
		map[string]any{"type": "field", "port": "25", "outboundTag": abuseBlockTag},
	)
	for _, rule := range rules {
		if item, ok := rule.(map[string]any); ok && stringValue(item["outboundTag"]) == abuseBlockTag {
			continue // added again above
		}
		kept = append(kept, rule)
	}
	routing["rules"] = kept
	raw["routing"] = routing

	// BitTorrent is recognised only on inbounds that sniff. Where an inbound
	// says nothing about sniffing it is turned on for routing only, which
	// leaves where its connections go untouched; an explicit choice is kept.
	for _, inbound := range listOfMaps(raw["inbounds"]) {
		if !abuseSniffedProtocols[strings.ToLower(stringValue(inbound["protocol"]))] {
			continue
		}
		if _, set := inbound["sniffing"]; set {
			continue
		}
		inbound["sniffing"] = map[string]any{
			"enabled": true, "destOverride": []any{"http", "tls", "quic"}, "routeOnly": true,
		}
	}
	return raw
}
