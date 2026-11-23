package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A country's HTTP config is a direct config with no TLS at all: VMess over
// plain TCP whose first bytes are an ordinary HTTP request to an Iranian site
// (TSinGate.HTTPHost, tgju.org by default), so a censor reading the line sees
// a domestic web request and no TLS to fingerprint. VMess encrypts what is
// inside. It uses the same address as the ⚡ and 💎 configs (a front's IPs,
// one picked at random per subscription when there are several), and is
// chosen per country.

// TSinHTTPSuffix ends the tag of a country's HTTP inbound.
const TSinHTTPSuffix = "-http"

// DefaultTSinHTTPHost is the site the HTTP configs name when none is set.
const DefaultTSinHTTPHost = "tgju.org"

// tsinHTTPRemark follows the country's name on an HTTP config.
const tsinHTTPRemark = " | همه اپراتورها"

// TSinHTTPInboundTag is the inbound that serves a country over VMess with an
// HTTP header.
func TSinHTTPInboundTag(country string) string {
	return TSinInboundTag(country) + TSinHTTPSuffix
}

// NormalizeTSinHTTPHost checks the site the HTTP configs name: a plain host
// name. Empty is the default.
func NormalizeTSinHTTPHost(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultTSinHTTPHost, nil
	}
	host, err := NormalizeCDNDomain(value)
	if err != nil || host == "" {
		return "", errors.New("the HTTP configs' Host must be a plain site name such as tgju.org, without http:// or a port")
	}
	return host, nil
}

func buildTSinHTTPInbound(tag string, port int, host string) map[string]any {
	return map[string]any{
		"tag":      tag,
		"listen":   "::",
		"port":     port,
		"protocol": "vmess",
		"settings": map[string]any{"clients": []any{}},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "none",
			"tcpSettings": map[string]any{
				"header": map[string]any{
					"type": "http",
					"request": map[string]any{
						"version": "1.1",
						"method":  "GET",
						"path":    []any{"/"},
						"headers": map[string]any{
							"Host":            []any{host},
							"User-Agent":      []any{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"},
							"Accept-Encoding": []any{"gzip, deflate"},
							"Connection":      []any{"keep-alive"},
							"Pragma":          "no-cache",
						},
					},
					"response": map[string]any{
						"version": "1.1",
						"status":  "200",
						"reason":  "OK",
						"headers": map[string]any{
							"Content-Type":      []any{"text/html; charset=utf-8"},
							"Transfer-Encoding": []any{"chunked"},
							"Connection":        []any{"keep-alive"},
							"Pragma":            "no-cache",
						},
					},
				},
			},
		},
		"sniffing": autoProvisionSniffing(),
	}
}

// tsinHTTPInboundHost is the site an existing HTTP inbound names, or "" when
// it is not one (another protocol or transport).
func tsinHTTPInboundHost(inbound map[string]any) string {
	if stringValue(inbound["protocol"]) != "vmess" {
		return ""
	}
	stream := mapValue(inbound["streamSettings"])
	if !strings.EqualFold(stringValue(stream["network"]), "tcp") {
		return ""
	}
	header := mapValue(mapValue(stream["tcpSettings"])["header"])
	if !strings.EqualFold(stringValue(header["type"]), "http") {
		return ""
	}
	hosts := interfaceSlice(mapValue(mapValue(header["request"])["headers"])["Host"])
	if len(hosts) == 0 {
		return ""
	}
	return strings.ToLower(stringValue(hosts[0]))
}

// ensureTSinHTTPInbound keeps or makes a country's HTTP inbound and points
// its config at the direct address. An existing inbound keeps its port unless
// the named site changed, which changes the link anyway.
func (r Repository) ensureTSinHTTPInbound(ctx context.Context, target string, country TSinCountry, gate TSinGate, used map[int]bool, ranges []portRange, portBusy func(int) bool) (TSinInbound, error) {
	tag := TSinHTTPInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportTCP, HTTP: true}
	host, err := NormalizeTSinHTTPHost(gate.HTTPHost)
	if err != nil {
		return entry, err
	}
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing != nil && tsinHTTPInboundHost(existing) != host {
		if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return entry, fmt.Errorf("rebuild %s: %w", tag, err)
		}
		existing = nil
	}
	if existing != nil {
		entry.Port, _ = parseConfigPort(existing["port"])
	} else {
		port, err := pickProvisionPort(nil, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		used[port] = true
		payload := buildTSinHTTPInbound(tag, port, host)
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (HTTP): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	address := strings.TrimSpace(gate.DirectAddress)
	if address == "" {
		address = "{SERVER_IP}"
	}
	// Host and path come from the inbound's header, so the row leaves them.
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = NULL, path = NULL, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'none' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinHTTPRemark, address, tag)
	return entry, err
}
