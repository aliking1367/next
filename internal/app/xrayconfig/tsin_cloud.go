package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// A country's Cloudflare XHTTP config reaches the server through Cloudflare,
// as VLESS over XHTTP with TLS, the transport that has lasted longest there:
// Cloudflare closed accounts carrying WebSocket within hours.
//
// Every country has a name of its own under one domain (germany.x.example.com),
// all pointing at the server through one wildcard record and one Origin Rule to
// one port. On that port a front reads the name the connection asks for (its
// SNI) and hands the connection to that country's inbound, which listens on
// loopback only. Splitting by name rather than by path is what makes this
// work behind a CDN: Cloudflare keeps a separate set of connections to the
// server for each name, so a connection never carries two countries, while a
// path is only seen on a connection's first request (see tsin_fastly.go).

// TSinCloudSuffix ends the tag of a country's Cloudflare XHTTP inbound.
const TSinCloudSuffix = "-cfx"

// TSinCloudFrontTag is the inbound on the shared port that splits by name.
const TSinCloudFrontTag = TSinInboundPrefix + "cfx-front"

// tsinCloudOutboundPrefix starts the outbounds that hand a front connection to
// a country's inbound. They start with TSinOutboundPrefix, so clearing the
// T.Sin routing clears them too.
const tsinCloudOutboundPrefix = TSinOutboundPrefix + "cfx-"

// tsinCloudDropTag takes a connection for a name that is not a country.
const tsinCloudDropTag = tsinCloudOutboundPrefix + "drop"

// DefaultTSinCloudPort is the server port the Origin Rule points at.
const DefaultTSinCloudPort = 24443

// tsinCloudRemark marks a Cloudflare XHTTP config in the subscription.
const tsinCloudRemark = " ☁️"

// TSinCloudInboundTag is the inbound that serves a country through Cloudflare
// over XHTTP.
func TSinCloudInboundTag(country string) string {
	return TSinInboundTag(country) + TSinCloudSuffix
}

// TSinCloudHost is a country's name under the domain: germany.x.example.com.
func TSinCloudHost(country, domain string) string {
	return strings.TrimPrefix(TSinInboundTag(country), TSinInboundPrefix) + "." + strings.ToLower(strings.TrimSpace(domain))
}

func buildTSinCloudInbound(tag string, port int, host string) (map[string]any, error) {
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
		"listen":   "127.0.0.1",
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
				// Every upload a short POST and the download one long GET: the
				// mode a CDN that buffers request bodies carries.
				"mode": "packet-up",
			},
			"tlsSettings": map[string]any{
				"serverName": host,
				"alpn":       []any{"h2", "http/1.1"},
				"minVersion": "1.2",
				// Self-signed: only Cloudflare sees it, and SSL mode Full does
				// not check it. Users see Cloudflare's own certificate.
				"certificates": []any{
					map[string]any{"certificate": certLines, "key": keyLines},
				},
			},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// ensureTSinCloudInbound keeps or makes a country's Cloudflare XHTTP inbound
// and points its config at the country's name. An existing inbound keeps its
// port, path and certificate unless the domain changed.
func (r Repository) ensureTSinCloudInbound(ctx context.Context, target string, country TSinCountry, gate TSinGate, used map[int]bool, ranges []portRange, portBusy func(int) bool) (TSinInbound, error) {
	tag := TSinCloudInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportXHTTP, Cloud: true}
	domain, err := NormalizeCDNDomain(gate.CloudDomain)
	if err != nil || domain == "" {
		return entry, errors.New("the Cloudflare XHTTP domain must be a plain domain such as x.example.com")
	}
	host := TSinCloudHost(country.Name, domain)
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	if existing != nil {
		stream := mapValue(existing["streamSettings"])
		sameHost := strings.EqualFold(stringValue(mapValue(stream["tlsSettings"])["serverName"]), host)
		if tsinInboundTransport(existing) != TSinTransportXHTTP || !sameHost || stringValue(existing["listen"]) != "127.0.0.1" {
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return entry, fmt.Errorf("rebuild %s: %w", tag, err)
			}
			existing = nil
		}
	}
	if existing != nil {
		entry.Port, _ = parseConfigPort(existing["port"])
	} else {
		port, err := pickProvisionPort(nil, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		used[port] = true
		payload, err := buildTSinCloudInbound(tag, port, host)
		if err != nil {
			return entry, fmt.Errorf("%s (Cloudflare XHTTP): %w", country.Name, err)
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (Cloudflare XHTTP): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	address := strings.TrimSpace(gate.CloudConnectAddress)
	if address == "" {
		address = host
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = 443, sni = ?, host = ?,
security = 'tls', alpn = 'h2,http/1.1', fingerprint = 'chrome' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinCloudRemark, address, host, host, tag)
	return entry, err
}

// applyTSinCloudFront rebuilds the front for the countries' Cloudflare XHTTP
// inbounds in the target's config: the front inbound itself (the panel's
// inbound records take no dokodemo-door, so it lives in the config only), a
// hand-over outbound per country and their rules, first, so no block rule
// sees the loopback hop. setTSinRouting clears all of it, so removing T.Sin
// or moving it to another server takes the front along.
func (r Repository) applyTSinCloudFront(ctx context.Context, target string, gate TSinGate, countries []TSinInbound) error {
	var cloud []TSinInbound
	for _, country := range countries {
		if country.Cloud {
			cloud = append(cloud, country)
		}
	}
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return err
	}
	inbounds, outbounds, rules, had := withoutTSinCloudFront(config)
	if len(cloud) == 0 && !had {
		return nil
	}
	port := gate.CloudPort
	if port <= 0 {
		port = DefaultTSinCloudPort
	}
	domain, _ := NormalizeCDNDomain(gate.CloudDomain)
	front := []any{}
	if len(cloud) > 0 {
		for _, item := range inbounds {
			if p, _ := parseConfigPort(mapValue(item)["port"]); p == port {
				return fmt.Errorf("port %d is already used by %q on this server; choose another Cloudflare XHTTP port", port, stringValue(mapValue(item)["tag"]))
			}
		}
		inbounds = append(inbounds, map[string]any{
			"tag": TSinCloudFrontTag, "listen": "::", "port": port, "protocol": "dokodemo-door",
			"settings": map[string]any{"address": "www.cloudflare.com", "port": 443, "network": "tcp"},
			"sniffing": map[string]any{"enabled": true, "destOverride": []any{"tls"}, "routeOnly": true},
		})
		for _, country := range cloud {
			tag := tsinCloudOutboundPrefix + strings.TrimPrefix(country.Tag, TSinInboundPrefix)
			outbounds = append(outbounds, map[string]any{
				"tag": tag, "protocol": "freedom",
				"settings": map[string]any{"redirect": fmt.Sprintf("127.0.0.1:%d", country.Port)},
			})
			front = append(front, map[string]any{
				"type": "field", "inboundTag": []any{TSinCloudFrontTag},
				"domain": []any{"full:" + TSinCloudHost(country.Country, domain)}, "outboundTag": tag,
			})
		}
		outbounds = append(outbounds, map[string]any{"tag": tsinCloudDropTag, "protocol": "blackhole"})
		front = append(front, map[string]any{"type": "field", "inboundTag": []any{TSinCloudFrontTag}, "outboundTag": tsinCloudDropTag})
	}
	routing := mapValue(config["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	routing["rules"] = append(front, rules...)
	config["routing"] = routing
	config["inbounds"] = inbounds
	config["outbounds"] = outbounds
	if _, err := r.SaveTargetRawConfig(ctx, target, config); err != nil {
		return fmt.Errorf("save the Cloudflare XHTTP front: %w", err)
	}
	return nil
}

// withoutTSinCloudFront is config's inbounds, outbounds and rules without the
// front's, and whether there were any.
func withoutTSinCloudFront(config map[string]any) (inbounds, outbounds, rules []any, had bool) {
	inbounds, outbounds, rules = []any{}, []any{}, []any{}
	for _, item := range interfaceSlice(config["inbounds"]) {
		if stringValue(mapValue(item)["tag"]) == TSinCloudFrontTag {
			had = true
			continue
		}
		inbounds = append(inbounds, item)
	}
	for _, item := range interfaceSlice(config["outbounds"]) {
		if strings.HasPrefix(stringValue(mapValue(item)["tag"]), tsinCloudOutboundPrefix) {
			had = true
			continue
		}
		outbounds = append(outbounds, item)
	}
	for _, item := range interfaceSlice(mapValue(config["routing"])["rules"]) {
		if strings.HasPrefix(stringValue(mapValue(item)["outboundTag"]), tsinCloudOutboundPrefix) {
			had = true
			continue
		}
		rules = append(rules, item)
	}
	return inbounds, outbounds, rules, had
}
