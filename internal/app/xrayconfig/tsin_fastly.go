package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A country's Fastly config reaches the server through Fastly's CDN, a second
// CDN beside Cloudflare, so a ban or a block on one leaves the other.
//
// It is VLESS over XHTTP in packet-up mode, the mode a CDN that buffers
// request bodies carries: every upload is a short POST and the download a
// long GET. TLS with a self-signed certificate protects the leg from Fastly
// to the server (the Fastly backend is set not to verify it); users see
// Fastly's own certificate.
//
// Each country has its own inbound on its own port, and Fastly a backend per
// country chosen by path. One port for all, split by path like the
// Cloudflare gate, does not work here: a CDN reuses its connections to the
// server across users, and the gate routes a connection by its first request
// only, so a later request for another country would reach the wrong one.

// TSinFastlySuffix ends the tag of a country's Fastly inbound.
const TSinFastlySuffix = "-fastly"

// tsinFastlyRemark marks a Fastly config in the subscription.
const tsinFastlyRemark = " 🅵"

// TSinFastlyInboundTag is the inbound that serves a country through Fastly.
func TSinFastlyInboundTag(country string) string {
	return TSinInboundTag(country) + TSinFastlySuffix
}

func buildTSinFastlyInbound(tag string, port int, host string) (map[string]any, error) {
	path, err := randomXHTTPPath()
	if err != nil {
		return nil, err
	}
	certLines, keyLines, err := generateSelfSignedCertLines(host, host)
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
			"network":  "xhttp",
			"security": "tls",
			"xhttpSettings": map[string]any{
				"path": path,
				"mode": "packet-up",
			},
			"tlsSettings": map[string]any{
				"serverName": host,
				// Fastly speaks HTTP/1.1 to its backends; h2 too, so a client
				// that reaches the port without Fastly is not refused.
				"alpn":       []any{"h2", "http/1.1"},
				"minVersion": "1.2",
				"certificates": []any{
					map[string]any{"certificate": certLines, "key": keyLines},
				},
			},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// ensureTSinFastlyInbound keeps or makes a country's Fastly inbound and
// points its config at the Fastly domain. An existing inbound keeps its port
// and path, so the Fastly backends set up for it keep working.
func (r Repository) ensureTSinFastlyInbound(ctx context.Context, target string, country TSinCountry, host string, used map[int]bool, ranges []portRange, portBusy func(int) bool) (TSinInbound, error) {
	tag := TSinFastlyInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportXHTTP, Fastly: true}
	host, err := NormalizeCDNDomain(host)
	if err != nil || host == "" {
		return entry, errors.New("the Fastly domain must be a plain domain such as name.global.ssl.fastly.net")
	}
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing != nil && tsinInboundTransport(existing) != TSinTransportXHTTP {
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
		payload, err := buildTSinFastlyInbound(tag, port, host)
		if err != nil {
			return entry, fmt.Errorf("%s (Fastly): %w", country.Name, err)
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (Fastly): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = 443, sni = ?, host = ?,
security = 'tls', alpn = 'h2,http/1.1', fingerprint = 'chrome' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinFastlyRemark, host, host, host, tag)
	return entry, err
}

// TSinFastlyBackend is what one Fastly backend needs: the country's port on
// the server and the path its requests start with.
type TSinFastlyBackend struct {
	Country string `json:"country"`
	Tag     string `json:"tag"`
	Port    int    `json:"port"`
	Path    string `json:"path"`
}

// TSinFastlyBackends lists the Fastly inbounds that exist now, for the setup
// guide and the check.
func (r Repository) TSinFastlyBackends(ctx context.Context, countries []string) []TSinFastlyBackend {
	var backends []TSinFastlyBackend
	for _, name := range countries {
		tag := TSinFastlyInboundTag(name)
		inbound, err := r.GetInbound(ctx, tag)
		if err != nil || inbound == nil {
			continue
		}
		port, _ := parseConfigPort(inbound["port"])
		path := stringValue(mapValue(mapValue(inbound["streamSettings"])["xhttpSettings"])["path"])
		if port <= 0 || strings.TrimSpace(path) == "" {
			continue
		}
		backends = append(backends, TSinFastlyBackend{Country: name, Tag: tag, Port: port, Path: path})
	}
	return backends
}
