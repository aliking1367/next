package xrayconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// Direct adds, in path mode, a second config for the country that reaches
	// the server's own IP over REALITY, with no Cloudflare in the way.
	Direct bool
	// Fastly adds, in path mode, a config for the country through Fastly's
	// CDN (see tsin_fastly.go).
	Fastly bool
	// TunnelPort adds, in path mode, a config for the country through the
	// Iranian tunnel on this port (see tsin_tunnel.go); 0 adds none.
	TunnelPort int
}

type TSinInbound struct {
	Tag       string `json:"tag"`
	Port      int    `json:"port"`
	Country   string `json:"country"`
	Transport string `json:"transport"`
	Created   bool   `json:"created"`
	// Direct marks the second, Cloudflare-free config of a country.
	Direct bool `json:"direct,omitempty"`
	// Fastly marks a country's config through Fastly.
	Fastly bool `json:"fastly,omitempty"`
	// Tunnel marks a country's config through the Iranian tunnel, on
	// TunnelPort.
	Tunnel     bool `json:"tunnel,omitempty"`
	TunnelPort int  `json:"tunnel_port,omitempty"`
}

// TSinDirectSuffix ends the tag of a country's direct inbound.
const TSinDirectSuffix = "-direct"

// TSinDirectInboundTag is the inbound that serves a country directly on the
// server's IP, beside its path-mode inbound behind Cloudflare.
func TSinDirectInboundTag(country string) string {
	return TSinInboundTag(country) + TSinDirectSuffix
}

// tsinDirectRemark marks a direct config in the subscription, so users can
// tell it from the Cloudflare one of the same country.
const tsinDirectRemark = " ⚡"

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
	// DirectAddress is where the direct configs point: an extra IP of the
	// server kept only for them, so the server's main IP never appears in a
	// user's config. Empty uses the server's own address.
	DirectAddress string
	// FastlyHost is the Fastly service's domain the Fastly configs point at;
	// empty adds none.
	FastlyHost string
	// TunnelHost is the Iranian tunnel's address the tunnel configs point at;
	// empty adds none.
	TunnelHost string
}

// tsinDirectPorts are the ports direct configs take, in order: the ones
// ordinary HTTPS sites use, so a direct config looks like one.
var tsinDirectPorts = []int{443, 8443, 2053, 2083, 2096, 9443}

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

// tsinWantsTunnel says whether a country gets a tunnel config: path mode, a
// tunnel address, and a port for it.
func tsinWantsTunnel(country TSinCountry, transport string, gate TSinGate) bool {
	return transport == TSinTransportPath && country.TunnelPort > 0 && strings.TrimSpace(gate.TunnelHost) != ""
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
		if country.Direct && transport == TSinTransportPath {
			wanted[TSinDirectInboundTag(country.Name)] = true
		}
		if country.Fastly && transport == TSinTransportPath && gate.FastlyHost != "" {
			wanted[TSinFastlyInboundTag(country.Name)] = true
		}
		if tsinWantsTunnel(country, transport, gate) {
			wanted[TSinTunnelInboundTag(country.Name)] = true
		}
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
		if tag == TSinGateTag || strings.HasPrefix(tag, TSinTunnelGatePrefix) {
			// Rebuilt below on every apply, since their fallbacks follow the countries.
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
		for _, country := range countries {
			if tsinWantsTunnel(country, transport, gate) {
				used[country.TunnelPort] = true
			}
		}
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

		if country.Direct && transport == TSinTransportPath {
			direct, err := r.ensureTSinDirectInbound(ctx, target, country, gate.DirectAddress, used, ranges, portBusy, catalog, usedDests)
			if err != nil {
				return nil, err
			}
			result = append(result, direct)
		}
		if country.Fastly && transport == TSinTransportPath && gate.FastlyHost != "" {
			fastly, err := r.ensureTSinFastlyInbound(ctx, target, country, gate.FastlyHost, used, ranges, portBusy)
			if err != nil {
				return nil, err
			}
			result = append(result, fastly)
		}
		if tsinWantsTunnel(country, transport, gate) {
			tunnel, err := r.ensureTSinTunnelInbound(ctx, target, country, gate.TunnelHost, used, ranges, portBusy)
			if err != nil {
				return nil, err
			}
			result = append(result, tunnel)
		}
	}

	if err := r.applyTSinGate(ctx, target, transport, gate, result); err != nil {
		return nil, err
	}
	if err := r.applyTSinTunnelGates(ctx, target, result); err != nil {
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

// ensureTSinDirectInbound keeps or makes a country's direct inbound: VLESS
// over REALITY with XHTTP on the server's own IP, the transport that held up
// best on Iranian networks without a CDN. It shares the country's exit, so
// it costs nothing on the T.Sin side.
func (r Repository) ensureTSinDirectInbound(ctx context.Context, target string, country TSinCountry, address string, used map[int]bool, ranges []portRange, portBusy func(int) bool, catalog Catalog, usedDests map[string]bool) (TSinInbound, error) {
	tag := TSinDirectInboundTag(country.Name)
	entry := TSinInbound{Tag: tag, Country: country.Name, Transport: TSinTransportXHTTP, Direct: true}
	existing, err := r.GetInbound(ctx, tag)
	if err != nil && !errors.Is(err, ErrInboundNotFound) {
		return entry, err
	}
	rebuild := existing != nil && tsinInboundTransport(existing) != TSinTransportXHTTP
	if existing != nil && !rebuild {
		// One made on a random port moves to an HTTPS-looking one once one is
		// free; one already on such a port stays, so links do not churn.
		port, _ := parseConfigPort(existing["port"])
		rebuild = portBusy != nil && !slices.Contains(tsinDirectPorts, port) && tsinDirectPortFree(used, ranges, portBusy)
	}
	if rebuild {
		if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
			return entry, fmt.Errorf("rebuild %s: %w", tag, err)
		}
		existing = nil
	}
	if existing != nil {
		entry.Port, _ = parseConfigPort(existing["port"])
	} else {
		// The HTTPS ports are tried only where it can be checked that nothing
		// else listens on them: Xray failing to bind one would take every
		// config on that server down. Otherwise a random high port, as before.
		var preferred []int
		if portBusy != nil {
			preferred = tsinDirectPorts
		}
		port, err := pickProvisionPort(preferred, used, ranges, portBusy)
		if err != nil {
			return entry, err
		}
		used[port] = true
		payload, err := buildAutoRealityXHTTP(autoProvisionBuild{
			tag: tag, port: port, catalog: catalog, usedDests: usedDests, usedPorts: used,
		})
		if err != nil {
			return entry, fmt.Errorf("%s (direct): %w", country.Name, err)
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return entry, fmt.Errorf("%s (direct): %w", country.Name, err)
		}
		entry.Port, entry.Created = port, true
	}
	if strings.TrimSpace(address) == "" {
		address = "{SERVER_IP}"
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET remark = ?, address = ?, port = NULL, sni = NULL, host = NULL,
security = 'inbound_default', alpn = 'none', fingerprint = 'none' WHERE inbound_tag = ?`,
		tsinHostRemark(country)+tsinDirectRemark, strings.TrimSpace(address), tag)
	return entry, err
}

func tsinDirectPortFree(used map[int]bool, ranges []portRange, portBusy func(int) bool) bool {
	for _, port := range tsinDirectPorts {
		if !isPortUsed(port, used, ranges) && !portBusy(port) {
			return true
		}
	}
	return false
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
		inbounds := []any{TSinInboundTag(country.Name)}
		if country.Direct {
			inbounds = append(inbounds, TSinDirectInboundTag(country.Name))
		}
		if country.Fastly {
			inbounds = append(inbounds, TSinFastlyInboundTag(country.Name))
		}
		if country.TunnelPort > 0 {
			inbounds = append(inbounds, TSinTunnelInboundTag(country.Name))
		}
		rules = append(rules, map[string]any{
			"type":        "field",
			"inboundTag":  inbounds,
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
		if country.Direct || country.Fastly || country.Tunnel {
			continue // reached on its own port, not through the gate
		}
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

// SetTSinGateHost points every country config that goes through the gate at
// another Cloudflare domain. Only the host rows change: the gate answers any
// name, because Cloudflare's Full mode does not check the origin's
// certificate, so nothing is sent to Xray and nobody reconnects. Users get the
// new domain when they update their subscription. It returns how many host
// rows moved.
func (r Repository) SetTSinGateHost(ctx context.Context, oldHost, newHost string) (int64, error) {
	newHost, err := NormalizeCDNDomain(newHost)
	if err != nil || newHost == "" {
		return 0, errors.New("enter a plain domain such as g2.example.com")
	}
	oldHost = strings.ToLower(strings.TrimSpace(oldHost))
	if oldHost == "" {
		return 0, errors.New("there is no domain in use to replace")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE hosts SET address = ?, sni = ?, host = ?
WHERE inbound_tag LIKE 'tsin-%' AND inbound_tag <> ? AND inbound_tag NOT LIKE ? AND inbound_tag NOT LIKE ? AND inbound_tag NOT LIKE ? AND LOWER(address) = ?`,
		newHost, newHost, newHost, TSinGateTag, "%"+TSinDirectSuffix, "%"+TSinFastlySuffix, "%"+TSinTunnelSuffix, oldHost)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
