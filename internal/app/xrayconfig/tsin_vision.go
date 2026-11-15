package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// A country's Vision config is the other direct config: VLESS over REALITY on
// plain TCP with the Vision flow, on the server's own address or a front's,
// like the direct (XHTTP) one. Its REALITY borrows the TLS of one chosen site
// (TSinGate.VisionSNI, www.hetzner.com by default) for every country, so a
// front on a Hetzner IP shows a Hetzner name: the pairing a sellers' front on
// Hetzner uses, which holds up where a stranger's name on that IP would not.
// Both kinds can be on at once, and each is chosen per country.

// TSinVisionSuffix ends the tag of a country's Vision inbound.
const TSinVisionSuffix = "-vision"

// DefaultTSinVisionSNI is the site whose TLS the Vision configs borrow when
// none is set.
const DefaultTSinVisionSNI = "www.hetzner.com"

// tsinVisionRemark marks a Vision config in the subscription.
const tsinVisionRemark = " 💎"

// TSinVisionInboundTag is the inbound that serves a country over TCP Vision.
func TSinVisionInboundTag(country string) string {
	return TSinInboundTag(country) + TSinVisionSuffix
}

// NormalizeTSinVisionSNI checks the site the Vision configs borrow: a plain
// host name. Empty is the default.
func NormalizeTSinVisionSNI(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultTSinVisionSNI, nil
	}
	host, err := NormalizeCDNDomain(value)
	if err != nil || host == "" {
		return "", errors.New("the Vision SNI must be a plain site name such as www.hetzner.com, without https:// or a port")
	}
	return host, nil
}

func buildTSinVisionInbound(tag string, port int, sni string) (map[string]any, error) {
	privateKey, _, err := generateRealityKeyPair()
	if err != nil {
		return nil, err
	}
	shortID, err := generateRealityShortIDHex()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      tag,
		"listen":   "::",
		"port":     port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "reality",
			"realitySettings": map[string]any{
				"show":        false,
				"dest":        sni + ":443",
				"xver":        0,
				"serverNames": []any{sni},
				"privateKey":  privateKey,
				"shortIds":    []any{shortID},
			},
			"tcpSettings": map[string]any{"header": map[string]any{"type": "none"}},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// tsinVisionInboundSNI is the site an existing Vision inbound borrows, or ""
// when it is not one (a different transport or no REALITY).
func tsinVisionInboundSNI(inbound map[string]any) string {
	stream := mapValue(inbound["streamSettings"])
	if !strings.EqualFold(stringValue(stream["network"]), "tcp") || !strings.EqualFold(stringValue(stream["security"]), "reality") {
		return ""
	}
	names := interfaceSlice(mapValue(stream["realitySettings"])["serverNames"])
	if len(names) == 0 {
		return ""
	}
	return strings.ToLower(stringValue(names[0]))
}

// ensureTSinVisionInbound keeps or makes a country's Vision inbound and points
// its config at the direct address. An existing inbound keeps its port and
// keys unless the borrowed site changed, which changes the link anyway.
func (r Repository) ensureTSinVisionInbound(ctx context.Context, target string, country TSinCountry, gate TSinGate, used map[int]bool, ranges []portRange, portBusy func(int) bool) (TSinInbound, error) {
	tag := TSinVisionInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportTCP, Vision: true}
	sni, err := NormalizeTSinVisionSNI(gate.VisionSNI)
	if err != nil {
		return entry, err
	}
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing != nil && tsinVisionInboundSNI(existing) != sni {
		if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return entry, fmt.Errorf("rebuild %s: %w", tag, err)
		}
		existing = nil
	}
	if existing != nil {
		entry.Port, _ = parseConfigPort(existing["port"])
	} else {
		// The HTTPS ports first, where it can be checked that nothing else
		// listens on them, as for the direct configs.
		var preferred []int
		if portBusy != nil {
			preferred = slices.Clone(tsinDirectPorts)
		}
		port, err := pickProvisionPort(preferred, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		used[port] = true
		payload, err := buildTSinVisionInbound(tag, port, sni)
		if err != nil {
			return entry, fmt.Errorf("%s (Vision): %w", country.Name, err)
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (Vision): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	address := strings.TrimSpace(gate.DirectAddress)
	if address == "" {
		address = "{SERVER_IP}"
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = NULL, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'chrome' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinVisionRemark, address, tag)
	return entry, err
}
