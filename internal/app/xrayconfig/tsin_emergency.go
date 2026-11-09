package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The emergency tunnel is for the hours T.Sin itself is down. Two inbounds,
// VMess and Shadowsocks over plain TCP, sit on the server that runs the
// countries; an admin's Iranian tunnel forwards to them, and their traffic
// leaves from that server directly, not through T.Sin. They are created once
// and then left alone: turning the tunnel on or off only shows or hides their
// hosts, so neither costs Xray a restart.
//
// Their tags do not start with TSinInboundPrefix, so applying the countries
// never mistakes them for a country it should retire.
const (
	TSinEmergencyVMessTag = "emergency-vmess"
	TSinEmergencySSTag    = "emergency-ss"
	tsinEmergencyOutbound = "emergency-direct"
)

// The emergency configs an admin shows: both, or one of them when a single
// tunnel port was bought, since one Iranian port reaches one port here.
const (
	TSinEmergencyBoth  = "both"
	TSinEmergencyVMess = "vmess"
	TSinEmergencySS    = "shadowsocks"
)

// NormalizeTSinEmergencyProtocols maps anything unknown to both.
func NormalizeTSinEmergencyProtocols(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TSinEmergencyVMess:
		return TSinEmergencyVMess
	case TSinEmergencySS, "ss":
		return TSinEmergencySS
	}
	return TSinEmergencyBoth
}

// TSinEmergencyTags are both emergency inbounds.
var TSinEmergencyTags = []string{TSinEmergencyVMessTag, TSinEmergencySSTag}

// TSinEmergencyPorts are the ports the emergency inbounds listen on, for the
// admin to point the Iranian tunnel at.
type TSinEmergencyPorts struct {
	VMess       int `json:"vmess"`
	Shadowsocks int `json:"shadowsocks"`
}

// EnsureTSinEmergency makes the emergency inbounds on target, or keeps them
// where they already are, and routes them straight out. Inbounds left on
// another target are moved, since only target's server can reach the internet
// for its countries' users.
func (r Repository) EnsureTSinEmergency(ctx context.Context, target string, portBusy func(int) bool) (TSinEmergencyPorts, error) {
	var ports TSinEmergencyPorts
	target, err := normalizeTargetID(firstNonEmptyString(target, MasterTargetID))
	if err != nil {
		return ports, err
	}
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return ports, err
	}
	used, ranges := extractUsedPorts(config)
	for _, tag := range TSinEmergencyTags {
		existing, err := r.GetInbound(ctx, tag)
		if err != nil && !errors.Is(err, ErrInboundNotFound) {
			return ports, err
		}
		if existing != nil {
			direct, err := r.directTargetsForInbound(ctx, tag)
			if err != nil {
				return ports, err
			}
			if len(direct) != 1 || direct[0] != target {
				if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
					return ports, fmt.Errorf("move %s: %w", tag, err)
				}
				existing = nil
			}
		}
		port := 0
		if existing != nil {
			port, _ = parseConfigPort(existing["port"])
		} else {
			if port, err = pickProvisionPort(nil, used, ranges, portBusy); err != nil {
				return ports, err
			}
			used[port] = true
			payload := buildTSinEmergencyInbound(tag, port)
			payload["targets"] = []any{target}
			if _, err := r.CreateInbound(ctx, payload); err != nil {
				return ports, fmt.Errorf("%s: %w", tag, err)
			}
		}
		if tag == TSinEmergencyVMessTag {
			ports.VMess = port
		} else {
			ports.Shadowsocks = port
		}
	}
	if config, err = r.GetTargetRawConfig(ctx, target); err != nil {
		return ports, err
	}
	if setTSinEmergencyRouting(config) {
		if _, err := r.SaveTargetRawConfig(ctx, target, config); err != nil {
			return ports, fmt.Errorf("route the emergency tunnel: %w", err)
		}
	}
	return ports, nil
}

// RemoveTSinEmergency takes the emergency inbounds and their route away.
func (r Repository) RemoveTSinEmergency(ctx context.Context) error {
	targets, err := r.ListConfigTargets(ctx)
	if err != nil {
		return err
	}
	for _, item := range targets {
		if item.Type == "node" && item.Mode != ConfigModeCustom {
			continue
		}
		config, err := r.GetTargetRawConfig(ctx, item.ID)
		if err != nil {
			return err
		}
		if stripTSinEmergencyRouting(config) {
			if _, err := r.SaveTargetRawConfig(ctx, item.ID, config); err != nil {
				return err
			}
		}
	}
	for _, tag := range TSinEmergencyTags {
		if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return err
		}
	}
	return nil
}

func buildTSinEmergencyInbound(tag string, port int) map[string]any {
	if tag == TSinEmergencySSTag {
		return map[string]any{
			"tag":      tag,
			"listen":   "::",
			"port":     port,
			"protocol": "shadowsocks",
			"settings": map[string]any{"clients": []any{}, "network": "tcp,udp"},
			"sniffing": autoProvisionSniffing(),
		}
	}
	return map[string]any{
		"tag":      tag,
		"listen":   "::",
		"port":     port,
		"protocol": "vmess",
		"settings": map[string]any{"clients": []any{}},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "none",
		},
		"sniffing": autoProvisionSniffing(),
	}
}

// setTSinEmergencyRouting sends the emergency inbounds out directly, first
// among the rules, through a freedom outbound of their own appended last, so
// the config's default way out stays what it was. It reports a change.
func setTSinEmergencyRouting(config map[string]any) bool {
	before := fmt.Sprint(config["routing"], config["outbounds"])
	stripTSinEmergencyRouting(config)
	outbounds := interfaceSlice(config["outbounds"])
	config["outbounds"] = append(outbounds, map[string]any{"tag": tsinEmergencyOutbound, "protocol": "freedom"})
	routing := mapValue(config["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	rules := []any{map[string]any{
		"type":        "field",
		"inboundTag":  []any{TSinEmergencyVMessTag, TSinEmergencySSTag},
		"outboundTag": tsinEmergencyOutbound,
	}}
	routing["rules"] = append(rules, interfaceSlice(routing["rules"])...)
	config["routing"] = routing
	return before != fmt.Sprint(config["routing"], config["outbounds"])
}

func stripTSinEmergencyRouting(config map[string]any) bool {
	changed := false
	outbounds := []any{}
	for _, outbound := range interfaceSlice(config["outbounds"]) {
		if stringValue(mapValue(outbound)["tag"]) == tsinEmergencyOutbound {
			changed = true
			continue
		}
		outbounds = append(outbounds, outbound)
	}
	routing := mapValue(config["routing"])
	rules := []any{}
	for _, rule := range interfaceSlice(routing["rules"]) {
		if stringValue(mapValue(rule)["outboundTag"]) == tsinEmergencyOutbound {
			changed = true
			continue
		}
		rules = append(rules, rule)
	}
	if !changed {
		return false
	}
	config["outbounds"] = outbounds
	if routing != nil {
		routing["rules"] = rules
		config["routing"] = routing
	}
	return true
}

// SetTSinEmergencyHosts points the emergency hosts at the Iranian tunnel and
// shows or hides them. A port of 0 keeps the inbound's own port, for a tunnel
// that forwards each port to the same number.
func (r Repository) SetTSinEmergencyHosts(ctx context.Context, address string, vmessPort, ssPort int, enabled bool, protocols string) error {
	protocols = NormalizeTSinEmergencyProtocols(protocols)
	for _, item := range []struct {
		tag, remark, protocol string
		port                  int
	}{
		{TSinEmergencyVMessTag, "🆘 تونل اضطراری VMess", TSinEmergencyVMess, vmessPort},
		{TSinEmergencySSTag, "🆘 تونل اضطراری Shadowsocks", TSinEmergencySS, ssPort},
	} {
		disabled := 1
		if enabled && (protocols == TSinEmergencyBoth || protocols == item.protocol) {
			disabled = 0
		}
		var port any
		if item.port > 0 {
			port = item.port
		}
		if _, err := r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = ?, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'none', is_disabled = ? WHERE inbound_tag = ?`,
			item.remark, strings.TrimSpace(address), port, disabled, item.tag); err != nil {
			return err
		}
	}
	return nil
}
