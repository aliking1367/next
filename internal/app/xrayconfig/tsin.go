package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// A T.Sin country is reached by sending a dedicated inbound's traffic out
// through a SOCKS outbound that lives inside the provider's WireGuard tunnel.
// One inbound per country is what lets a user choose the exit by choosing a
// config: Xray decides the route by inbound tag, and a user's clients cannot
// otherwise say which country they want.
//
// Everything the panel creates for this carries a fixed prefix, so applying,
// re-applying and removing only ever touch its own entries.
const (
	TSinInboundPrefix  = "tsin-"
	TSinOutboundPrefix = "tsin-out-"
)

type TSinCountry struct {
	Port    int
	Name    string
	Code    string
	Address string
}

type TSinInbound struct {
	Tag       string `json:"tag"`
	Port      int    `json:"port"`
	Country   string `json:"country"`
	Transport string `json:"transport"`
	Created   bool   `json:"created"`
}

// Country inbounds are VLESS over REALITY on one of two transports. TCP is
// the classic one; XHTTP is HTTP-shaped and in practice survived on Iranian
// networks where long TCP-REALITY connections kept being cut every few
// minutes.
const (
	TSinTransportTCP   = "tcp"
	TSinTransportXHTTP = "xhttp"
	// TSinTransportPath puts every country behind one TLS gate on one domain,
	// told apart by URL path. Fronted by Cloudflare, each user config is the
	// same domain on port 443 with a different path, and the server's own IP
	// never appears in it.
	TSinTransportPath = "path"

	// TSinGateTag is the inbound that receives every country's traffic in
	// path mode and hands it to that country's inbound by path.
	TSinGateTag = "tsin-gate"
	// DefaultTSinGatePort is where Cloudflare reaches the gate; an Origin Rule
	// maps the domain's 443 to it.
	DefaultTSinGatePort = 2087
)

// NormalizeTSinTransport maps anything unknown to TCP, which is what every
// panel used before the choice existed.
func NormalizeTSinTransport(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case TSinTransportXHTTP:
		return TSinTransportXHTTP
	case TSinTransportPath, "ws":
		return TSinTransportPath
	}
	return TSinTransportTCP
}

func tsinInboundTransport(inbound map[string]any) string {
	return NormalizeTSinTransport(stringValue(mapValue(inbound["streamSettings"])["network"]))
}

func tsinInboundPath(inbound map[string]any) string {
	return stringValue(mapValue(mapValue(inbound["streamSettings"])["wsSettings"])["path"])
}

// TSinGate is where path mode is reached: the Cloudflare-proxied domain and
// the origin port the gate listens on.
type TSinGate struct {
	Host string
	Port int
}

// buildTSinPathInbound is one country in path mode: VLESS over WebSocket on
// loopback only. Nothing reaches it except the gate's fallback for its path.
func buildTSinPathInbound(in autoProvisionBuild) (map[string]any, error) {
	path, err := randomXHTTPPath()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      in.tag,
		"listen":   "127.0.0.1",
		"port":     in.port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network":    "ws",
			"security":   "none",
			"wsSettings": map[string]any{"path": path},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// buildTSinGate is the public side of path mode: VLESS over TLS whose only
// job is its fallbacks. It has no users, so every connection falls back and
// Xray picks the country inbound by the request path. The certificate is
// self-signed because only Cloudflare sees it (SSL mode Full accepts that);
// users see Cloudflare's own valid certificate.
func buildTSinGate(gate TSinGate, fallbacks []any) (map[string]any, error) {
	certLines, keyLines, err := generateSelfSignedCertLines(gate.Host, gate.Host)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      TSinGateTag,
		"listen":   "::",
		"port":     gate.Port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{},
			"decryption": "none",
			"fallbacks":  fallbacks,
		},
		"streamSettings": map[string]any{
			"network":  "tcp",
			"security": "tls",
			"tlsSettings": map[string]any{
				"serverName": gate.Host,
				"alpn":       []any{"http/1.1"},
				"minVersion": "1.2",
				"certificates": []any{
					map[string]any{"certificate": certLines, "key": keyLines},
				},
			},
		},
	}, nil
}

// TSinInboundTag is the inbound that serves one country.
func TSinInboundTag(country string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(country) {
		switch {
		case unicode.IsLetter(r) && r < unicode.MaxASCII, unicode.IsDigit(r):
			out.WriteRune(r)
		case out.Len() > 0 && !strings.HasSuffix(out.String(), "-"):
			out.WriteByte('-')
		}
	}
	return TSinInboundPrefix + strings.Trim(out.String(), "-")
}

func TSinOutboundTag(port int) string { return fmt.Sprintf("%s%d", TSinOutboundPrefix, port) }

// TSinFlag renders an ISO 3166 code as its flag emoji.
func TSinFlag(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 2 {
		return ""
	}
	var out strings.Builder
	for _, r := range code {
		if r < 'A' || r > 'Z' {
			return ""
		}
		out.WriteRune(0x1F1E6 + (r - 'A'))
	}
	return out.String()
}

func tsinHostRemark(country TSinCountry) string {
	name := country.Name
	if country.Code == "" {
		return name
	}
	return strings.TrimSpace(TSinFlag(country.Code) + " " + name)
}

// ApplyTSin makes the given target serve exactly the given countries. The
// target is "master" or "node:<id>"; the tunnel the exits sit behind has to
// exist on whichever machine runs that target's Xray.
func (r Repository) ApplyTSin(ctx context.Context, target, transport string, gate TSinGate, countries []TSinCountry, portBusy func(int) bool) ([]TSinInbound, error) {
	if len(countries) == 0 {
		return nil, errors.New("choose at least one country")
	}
	transport = NormalizeTSinTransport(transport)
	if transport == TSinTransportPath {
		host, err := NormalizeCDNDomain(gate.Host)
		if err != nil || host == "" {
			return nil, errors.New("path mode needs the Cloudflare domain the countries are served on, for example g1.example.com")
		}
		gate.Host = host
		if gate.Port <= 0 {
			gate.Port = DefaultTSinGatePort
		}
		if gate.Port > 65535 {
			return nil, errors.New("the gate port must be between 1 and 65535")
		}
	}
	target, err := normalizeTargetID(firstNonEmptyString(target, MasterTargetID))
	if err != nil {
		return nil, err
	}
	// Routing on every other target goes first, while its inbounds still exist:
	// a config left with no inbounds is rejected, and the rules must not outlive
	// the exits they point at.
	if err := r.stripTSinRouting(ctx, target); err != nil {
		return nil, err
	}
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, country := range countries {
		wanted[TSinInboundTag(country.Name)] = true
	}
	// Every T.Sin inbound anywhere that is not wanted on this target goes: a
	// country dropped from the list, or one left on the machine the service
	// used to run on. Keeping a stale copy would leave a config pointing at an
	// exit that no longer exists there.
	all, err := r.CollectInboundTags(ctx)
	if err != nil {
		return nil, err
	}
	for tag := range all {
		if !strings.HasPrefix(tag, TSinInboundPrefix) {
			continue
		}
		direct, err := r.directTargetsForInbound(ctx, tag)
		if err != nil {
			return nil, err
		}
		if tag == TSinGateTag {
			// Rebuilt below on every apply, since its fallbacks follow the countries.
			continue
		}
		if wanted[tag] && len(direct) == 1 && direct[0] == target {
			continue
		}
		if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return nil, fmt.Errorf("retire %s: %w", tag, err)
		}
	}
	// Re-read so ports freed above are counted as free and the others as taken.
	if config, err = r.GetTargetRawConfig(ctx, target); err != nil {
		return nil, err
	}
	used, ranges := extractUsedPorts(config)
	if transport == TSinTransportPath {
		used[gate.Port] = true
	}
	catalog := r.loadCatalog(ctx)
	usedDests := map[string]bool{}

	var result []TSinInbound
	for _, country := range countries {
		tag := TSinInboundTag(country.Name)
		existing, err := r.GetInbound(ctx, tag)
		if err != nil && !errors.Is(err, ErrInboundNotFound) {
			return nil, err
		}
		entry := TSinInbound{Tag: tag, Country: country.Name, Transport: transport}
		if existing != nil && tsinInboundTransport(existing) != transport {
			// Switching transport changes the link itself, so the inbound is
			// rebuilt rather than edited in place.
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return nil, fmt.Errorf("rebuild %s: %w", tag, err)
			}
			existing = nil
		}
		if existing != nil {
			entry.Port, _ = parseConfigPort(existing["port"])
		} else {
			port, err := pickProvisionPort(nil, used, ranges, portBusy)
			if err != nil {
				return nil, err
			}
			used[port] = true
			build := buildAutoRealityVision
			switch transport {
			case TSinTransportXHTTP:
				build = buildAutoRealityXHTTP
			case TSinTransportPath:
				build = buildTSinPathInbound
			}
			payload, err := build(autoProvisionBuild{
				tag: tag, port: port, catalog: catalog, usedDests: usedDests, usedPorts: used,
			})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", country.Name, err)
			}
			payload["targets"] = []any{target}
			if _, err := r.CreateInbound(ctx, payload); err != nil {
				return nil, fmt.Errorf("%s: %w", country.Name, err)
			}
			entry.Port, entry.Created = port, true
		}
		if err := r.setTSinHost(ctx, tag, tsinHostRemark(country), transport, gate); err != nil {
			return nil, err
		}
		result = append(result, entry)
	}

	if err := r.applyTSinGate(ctx, target, transport, gate, result); err != nil {
		return nil, err
	}
	config, err = r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return nil, err
	}
	setTSinRouting(config, countries)
	if _, err := r.SaveTargetRawConfig(ctx, target, config); err != nil {
		return nil, fmt.Errorf("save routing: %w", err)
	}
	return result, nil
}

// RemoveTSin takes every inbound, outbound and routing rule the panel made
// for T.Sin out of every config.
//
// Routing goes first: a config with no inbounds left is rejected as invalid,
// and on a panel whose only inbounds are T.Sin's the last deletion would
// otherwise leave the rules behind with nothing able to save the config.
func (r Repository) RemoveTSin(ctx context.Context) error {
	if err := r.stripTSinRouting(ctx, ""); err != nil {
		return err
	}
	all, err := r.CollectInboundTags(ctx)
	if err != nil {
		return err
	}
	for tag := range all {
		if strings.HasPrefix(tag, TSinInboundPrefix) {
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return fmt.Errorf("remove %s: %w", tag, err)
			}
		}
	}
	return nil
}

// stripTSinRouting removes the T.Sin outbounds and rules from every config
// that holds its own, except keep ("" keeps none). A node that follows the
// master has no config of its own: saving one would pin it to a copy, so only
// the master is touched on its behalf.
func (r Repository) stripTSinRouting(ctx context.Context, keep string) error {
	targets, err := r.ListConfigTargets(ctx)
	if err != nil {
		return err
	}
	for _, item := range targets {
		if item.ID == keep || (item.Type == "node" && item.Mode != ConfigModeCustom) {
			continue
		}
		config, err := r.GetTargetRawConfig(ctx, item.ID)
		if err != nil {
			return err
		}
		if !hasTSinRouting(config) {
			continue
		}
		setTSinRouting(config, nil)
		if _, err := r.SaveTargetRawConfig(ctx, item.ID, config); err != nil {
			return fmt.Errorf("clean %s: %w", item.ID, err)
		}
	}
	return nil
}

func hasTSinRouting(config map[string]any) bool {
	for _, outbound := range interfaceSlice(config["outbounds"]) {
		if strings.HasPrefix(stringValue(mapValue(outbound)["tag"]), TSinOutboundPrefix) {
			return true
		}
	}
	return false
}

// setTSinRouting replaces the panel's T.Sin outbounds and rules in config.
// The rules go first: they match one specific inbound, so they can never
// capture traffic meant for anything else, while a catch-all rule above them
// would capture theirs.
func setTSinRouting(config map[string]any, countries []TSinCountry) {
	outbounds := []any{}
	for _, outbound := range interfaceSlice(config["outbounds"]) {
		if !strings.HasPrefix(stringValue(mapValue(outbound)["tag"]), TSinOutboundPrefix) {
			outbounds = append(outbounds, outbound)
		}
	}
	routing := mapValue(config["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	rules := []any{}
	for _, country := range countries {
		address := country.Address
		if address == "" {
			address = "10.0.0.1"
		}
		outbounds = append(outbounds, map[string]any{
			"tag":      TSinOutboundTag(country.Port),
			"protocol": "socks",
			"settings": map[string]any{
				"servers": []any{map[string]any{"address": address, "port": country.Port}},
			},
		})
		rules = append(rules, map[string]any{
			"type":        "field",
			"inboundTag":  []any{TSinInboundTag(country.Name)},
			"outboundTag": TSinOutboundTag(country.Port),
		})
	}
	for _, rule := range interfaceSlice(routing["rules"]) {
		if !strings.HasPrefix(stringValue(mapValue(rule)["outboundTag"]), TSinOutboundPrefix) {
			rules = append(rules, rule)
		}
	}
	routing["rules"] = rules
	if len(outbounds) == 0 {
		// A config must keep one outbound; without this, removing T.Sin from a
		// panel that had nothing else would leave a config Xray refuses to load.
		outbounds = append(outbounds, map[string]any{"tag": "direct", "protocol": "freedom"})
	}
	config["outbounds"] = outbounds
	config["routing"] = routing
}

// setTSinHost points a country's host row at where users reach it. In path
// mode that is the gate's domain on 443 over TLS; otherwise the node's own
// address with the inbound's own settings, which also undoes path mode.
func (r Repository) setTSinHost(ctx context.Context, tag, remark, transport string, gate TSinGate) error {
	if transport == TSinTransportPath {
		_, err := r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = 443, sni = ?, host = ?,
security = 'tls', alpn = 'http/1.1', fingerprint = 'chrome' WHERE inbound_tag = ?`,
			remark, gate.Host, gate.Host, gate.Host, tag)
		return err
	}
	_, err := r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = '{SERVER_IP}', port = NULL, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'none' WHERE inbound_tag = ?`, remark, tag)
	return err
}

// applyTSinGate rebuilds the gate for the countries just applied, or removes
// it when path mode is off.
func (r Repository) applyTSinGate(ctx context.Context, target, transport string, gate TSinGate, countries []TSinInbound) error {
	if _, err := r.DeleteInbound(ctx, TSinGateTag); err != nil && !errors.Is(err, ErrInboundNotFound) {
		return fmt.Errorf("retire %s: %w", TSinGateTag, err)
	}
	if transport != TSinTransportPath {
		return nil
	}
	fallbacks := make([]any, 0, len(countries))
	for _, country := range countries {
		inbound, err := r.GetInbound(ctx, country.Tag)
		if err != nil {
			return err
		}
		path := tsinInboundPath(inbound)
		if path == "" {
			return fmt.Errorf("%s has no path", country.Tag)
		}
		fallbacks = append(fallbacks, map[string]any{"path": path, "dest": country.Port, "xver": 0})
	}
	current, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return err
	}
	for _, inbound := range listOfMaps(current["inbounds"]) {
		if port, _ := parseConfigPort(inbound["port"]); port == gate.Port {
			return fmt.Errorf("port %d is already used by %q on this server; choose another gate port", gate.Port, stringValue(inbound["tag"]))
		}
	}
	payload, err := buildTSinGate(gate, fallbacks)
	if err != nil {
		return err
	}
	payload["targets"] = []any{target}
	if _, err := r.CreateInbound(ctx, payload); err != nil {
		return fmt.Errorf("%s: %w", TSinGateTag, err)
	}
	// The gate is not something a user connects to by itself: with no users
	// it only forwards, so it gets no host of its own.
	_, err = r.db.ExecContext(ctx, `DELETE FROM hosts WHERE inbound_tag = ?`, TSinGateTag)
	return err
}
