package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A T.Sin licence works on one server only, so a second licence means a
// second server. The direct configs (⚡, 💎 and HTTP) can then run on that
// server (TSinGate.DirectTarget) and leave through its own tunnel, while the
// countries' other configs stay where they are. Both tunnels expose the
// countries at the same proxy address, so the routing is the same on both.
//
// Moving a config keeps its inbound as it is (port, keys, path), so users'
// links do not change; only the machine answering them does.

// tsinMovable says whether a T.Sin inbound is one of the direct configs that
// follow the direct target.
func tsinMovable(tag string) bool {
	if !strings.HasPrefix(tag, TSinInboundPrefix) || strings.HasPrefix(tag, TSinLabPrefix) {
		return false
	}
	return strings.HasSuffix(tag, TSinDirectSuffix) || strings.HasSuffix(tag, TSinVisionSuffix) || strings.HasSuffix(tag, TSinHTTPSuffix) || strings.HasSuffix(tag, TSinTLSSuffix)
}

// moveTSinInbound puts an existing inbound on target alone, as it is. When the
// port is taken there it reports false, and the caller makes a new one.
func (r Repository) moveTSinInbound(ctx context.Context, tag string, existing map[string]any, target string) (map[string]any, bool, error) {
	current, err := r.directTargetsForInbound(ctx, tag)
	if err != nil {
		return nil, false, err
	}
	if len(current) == 1 && current[0] == target {
		return existing, true, nil
	}
	payload := deepCopyMap(existing)
	for _, key := range []string{"effective_targets", "uplink", "downlink", "usage_coefficient"} {
		delete(payload, key)
	}
	payload["targets"] = []any{target}
	result, err := r.UpdateInbound(ctx, tag, payload)
	if err != nil {
		if errors.Is(err, ErrInvalidInbound) || strings.Contains(strings.ToLower(err.Error()), "port") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("move %s to %s: %w", tag, target, err)
	}
	return result.Inbound, true, nil
}

// keepOrMoveTSinInbound is the common start of every direct config: the
// existing inbound, moved to target if it runs elsewhere, or nil when there is
// none (or it could not move and was removed, to be made again).
func (r Repository) keepOrMoveTSinInbound(ctx context.Context, tag string, existing map[string]any, target string) (map[string]any, error) {
	if existing == nil {
		return nil, nil
	}
	moved, ok, err := r.moveTSinInbound(ctx, tag, existing, target)
	if err != nil {
		return nil, err
	}
	if ok {
		return moved, nil
	}
	if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
		return nil, fmt.Errorf("rebuild %s: %w", tag, err)
	}
	return nil, nil
}

// tsinRoutingSplit is which of a country's inbounds a config's routing
// covers: the countries' server covers all but the moved ones, the direct
// server only those.
type tsinRoutingSplit int

const (
	tsinRouteAll tsinRoutingSplit = iota
	tsinRouteMain
	tsinRouteDirect
)

// tsinCountryInbounds lists a country's inbound tags that split covers.
func tsinCountryInbounds(country TSinCountry, split tsinRoutingSplit) []any {
	direct := split != tsinRouteMain
	main := split != tsinRouteDirect
	var inbounds []any
	if main {
		inbounds = append(inbounds, TSinInboundTag(country.Name))
	}
	if direct && country.Direct {
		inbounds = append(inbounds, TSinDirectInboundTag(country.Name))
	}
	if direct && country.Vision {
		inbounds = append(inbounds, TSinVisionInboundTag(country.Name))
	}
	if direct && country.HTTP {
		inbounds = append(inbounds, TSinHTTPInboundTag(country.Name))
	}
	if direct && country.TLS {
		inbounds = append(inbounds, TSinTLSInboundTag(country.Name))
	}
	if main && country.Fastly {
		inbounds = append(inbounds, TSinFastlyInboundTag(country.Name))
	}
	if main && country.Cloud {
		inbounds = append(inbounds, TSinCloudInboundTag(country.Name))
	}
	if main && country.TunnelPort > 0 {
		inbounds = append(inbounds, TSinTunnelInboundTag(country.Name))
	}
	return inbounds
}

// tsinDirectCountries is the countries with any direct config, for the direct
// server's routing.
func tsinDirectCountries(countries []TSinCountry) []TSinCountry {
	var out []TSinCountry
	for _, country := range countries {
		if country.Direct || country.Vision || country.HTTP || country.TLS {
			out = append(out, country)
		}
	}
	return out
}
