package xrayconfig

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// The protocol lab finds out which protocol a network lets through to the
// countries' server (or the front server before it): it makes a handful of
// test inbounds, each a different way of hiding VLESS, all leaving through
// the Best ping exit, with one test client of their own. They are in no
// service, so no user gets them; the admin tests them by their links and
// removes them. Applying the countries removes them too, since every T.Sin
// inbound that is not asked for goes.

// TSinLabPrefix starts every lab inbound's tag; the node controller keeps
// their own test client (see nodecontroller.includeDBUsers).
const TSinLabPrefix = TSinInboundPrefix + "lab-"

// TSinLabTest is one lab inbound and the link that tests it.
type TSinLabTest struct {
	Tag   string `json:"tag"`
	Name  string `json:"name"`
	About string `json:"about"`
	Port  int    `json:"port"`
	Link  string `json:"link"`
}

type tsinLabRecipe struct {
	key, name, about string
	build            func(tag string, port int, client map[string]any) (map[string]any, error)
}

func tsinLabClient(id string) map[string]any {
	return map[string]any{"id": id, "email": "lab@tsin", "flow": ""}
}

func tsinLabRecipes() []tsinLabRecipe {
	vless := func(tag string, port int, client map[string]any, stream map[string]any) map[string]any {
		return map[string]any{
			"tag": tag, "listen": "::", "port": port, "protocol": "vless",
			"settings":       map[string]any{"clients": []any{client}, "decryption": "none"},
			"streamSettings": stream,
			"sniffing":       autoProvisionSniffing(),
		}
	}
	reality := func(dest string, names ...string) (map[string]any, error) {
		privateKey, _, err := generateRealityKeyPair()
		if err != nil {
			return nil, err
		}
		shortID, err := generateRealityShortIDHex()
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"show": false, "dest": dest, "xver": 0, "serverNames": stringsToAny(names),
			"privateKey": privateKey, "shortIds": []any{shortID},
		}, nil
	}
	return []tsinLabRecipe{
		{"http", "TCP + HTTP header", "VLESS without TLS that looks like plain HTTP to play.google.com (what sellers use for Irancell and fixed lines)",
			func(tag string, port int, client map[string]any) (map[string]any, error) {
				return vless(tag, port, client, map[string]any{
					"network": "tcp", "security": "none",
					"tcpSettings": map[string]any{"header": map[string]any{
						"type": "http",
						"request": map[string]any{
							"version": "1.1", "method": "GET", "path": []any{"/"},
							"headers": map[string]any{"Host": []any{"play.google.com"}},
						},
					}},
				}), nil
			}},
		{"vmess-http", "VMess + HTTP header", "Looks like plain HTTP to play.google.com like test 1, but VMess encrypts what is inside, so the operator sees neither the sites nor the user",
			func(tag string, port int, client map[string]any) (map[string]any, error) {
				return map[string]any{
					"tag": tag, "listen": "::", "port": port, "protocol": "vmess",
					"settings": map[string]any{"clients": []any{map[string]any{"id": client["id"], "email": client["email"]}}},
					"streamSettings": map[string]any{
						"network": "tcp", "security": "none",
						"tcpSettings": map[string]any{"header": map[string]any{
							"type": "http",
							"request": map[string]any{
								"version": "1.1", "method": "GET", "path": []any{"/"},
								"headers": map[string]any{"Host": []any{"play.google.com"}},
							},
						}},
					},
					"sniffing": autoProvisionSniffing(),
				}, nil
			}},
		{"xhttp-plain", "XHTTP, no TLS", "XHTTP over plain HTTP, no encryption layer of its own",
			func(tag string, port int, client map[string]any) (map[string]any, error) {
				path, err := randomXHTTPPath()
				if err != nil {
					return nil, err
				}
				return vless(tag, port, client, map[string]any{
					"network": "xhttp", "security": "none",
					"xhttpSettings": map[string]any{"path": path, "mode": "auto"},
				}), nil
			}},
		{"reality-gitlab", "REALITY XHTTP gitlab.io", "REALITY with the SNI and Firefox fingerprint a seller uses on Hamrah Aval",
			func(tag string, port int, client map[string]any) (map[string]any, error) {
				settings, err := reality("gitlab.io:443", "gitlab.io")
				if err != nil {
					return nil, err
				}
				path, err := randomXHTTPPath()
				if err != nil {
					return nil, err
				}
				return vless(tag, port, client, map[string]any{
					"network": "xhttp", "security": "reality", "realitySettings": settings,
					"xhttpSettings": map[string]any{"path": path, "mode": "auto"},
				}), nil
			}},
		{"reality-tcp", "REALITY TCP play.google.com", "REALITY on plain TCP with the SNI play.google.com",
			func(tag string, port int, client map[string]any) (map[string]any, error) {
				settings, err := reality("play.google.com:443", "play.google.com")
				if err != nil {
					return nil, err
				}
				return vless(tag, port, client, map[string]any{
					"network": "tcp", "security": "reality", "realitySettings": settings,
				}), nil
			}},
		{"tls-xhttp", "TLS XHTTP", "Ordinary TLS with the server's own certificate, which the link pins",
			func(tag string, port int, client map[string]any) (map[string]any, error) {
				path, err := randomXHTTPPath()
				if err != nil {
					return nil, err
				}
				certLines, keyLines, err := generateSelfSignedCertLines("www.speedtest.net", "www.speedtest.net")
				if err != nil {
					return nil, err
				}
				return vless(tag, port, client, map[string]any{
					"network": "xhttp", "security": "tls",
					"xhttpSettings": map[string]any{"path": path, "mode": "auto"},
					"tlsSettings": map[string]any{
						"serverName": "www.speedtest.net", "alpn": []any{"h2", "http/1.1"}, "minVersion": "1.2",
						"certificates": []any{map[string]any{"certificate": certLines, "key": keyLines}},
					},
				}), nil
			}},
	}
}

// tsinLabExit is the T.Sin outbound the Best ping country leaves through,
// or the first country's when there is no Best ping.
func tsinLabExit(config map[string]any) string {
	first := ""
	for _, rule := range listOfMaps(mapValue(config["routing"])["rules"]) {
		outbound := stringValue(rule["outboundTag"])
		if !strings.HasPrefix(outbound, TSinOutboundPrefix) {
			continue
		}
		for _, tag := range interfaceSlice(rule["inboundTag"]) {
			name := stringValue(tag)
			if strings.HasPrefix(name, TSinLabPrefix) {
				continue
			}
			if name == TSinInboundTag("Best ping") {
				return outbound
			}
			if first == "" {
				first = outbound
			}
		}
	}
	return first
}

// EnsureTSinLab makes the lab on target, or keeps the one there, and returns
// its tests with links to address.
func (r Repository) EnsureTSinLab(ctx context.Context, target, address string, portBusy func(int) bool) ([]TSinLabTest, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, errors.New("there is no address to test: set the front server's IP in the ⚡ section, or serve the countries from a node")
	}
	if existing, err := r.TSinLabTests(ctx, target, address); err == nil && len(existing) > 0 {
		if len(existing) == len(tsinLabRecipes()) {
			return existing, nil
		}
		// A lab made by an older version lacks the newer tests: make it again.
		if err := r.RemoveTSinLab(ctx, target); err != nil {
			return nil, err
		}
	}
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return nil, err
	}
	exit := tsinLabExit(config)
	if exit == "" {
		return nil, errors.New("apply the countries first; the lab leaves through their Best ping exit")
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	idBytes[6] = idBytes[6]&0x0f | 0x40
	idBytes[8] = idBytes[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", idBytes[0:4], idBytes[4:6], idBytes[6:8], idBytes[8:10], idBytes[10:16])
	used, ranges := extractUsedPorts(config)
	var tags []any
	for _, recipe := range tsinLabRecipes() {
		port, err := pickProvisionPort(nil, used, ranges, portBusy)
		if err != nil {
			return nil, err
		}
		used[port] = true
		tag := TSinLabPrefix + recipe.key
		payload, err := recipe.build(tag, port, tsinLabClient(id))
		if err != nil {
			return nil, err
		}
		payload["targets"] = []any{target}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return nil, fmt.Errorf("lab %s: %w", recipe.name, err)
		}
		tags = append(tags, tag)
	}
	if config, err = r.GetTargetRawConfig(ctx, target); err != nil {
		return nil, err
	}
	routing := mapValue(config["routing"])
	if routing == nil {
		routing = map[string]any{}
	}
	rules := []any{map[string]any{"type": "field", "inboundTag": tags, "outboundTag": exit}}
	for _, rule := range interfaceSlice(routing["rules"]) {
		if !tsinLabRule(mapValue(rule)) {
			rules = append(rules, rule)
		}
	}
	routing["rules"] = rules
	config["routing"] = routing
	// Storing an inbound keeps only reverse clients, so the test client goes
	// in here, with the routing, through the raw config.
	for _, inbound := range listOfMaps(config["inbounds"]) {
		if strings.HasPrefix(stringValue(inbound["tag"]), TSinLabPrefix) {
			settings := mapValue(inbound["settings"])
			if settings == nil {
				settings = map[string]any{}
			}
			client := tsinLabClient(id)
			if stringValue(inbound["protocol"]) == "vmess" {
				delete(client, "flow")
			}
			settings["clients"] = []any{client}
			inbound["settings"] = settings
		}
	}
	if _, err := r.SaveTargetRawConfig(ctx, target, config); err != nil {
		return nil, fmt.Errorf("save lab routing: %w", err)
	}
	return r.TSinLabTests(ctx, target, address)
}

func tsinLabRule(rule map[string]any) bool {
	tags := interfaceSlice(rule["inboundTag"])
	if len(tags) == 0 {
		return false
	}
	for _, tag := range tags {
		if !strings.HasPrefix(stringValue(tag), TSinLabPrefix) {
			return false
		}
	}
	return true
}

// RemoveTSinLab takes the lab off target.
func (r Repository) RemoveTSinLab(ctx context.Context, target string) error {
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return err
	}
	routing := mapValue(config["routing"])
	if routing != nil {
		rules := []any{}
		for _, rule := range interfaceSlice(routing["rules"]) {
			if !tsinLabRule(mapValue(rule)) {
				rules = append(rules, rule)
			}
		}
		routing["rules"] = rules
		config["routing"] = routing
		if _, err := r.SaveTargetRawConfig(ctx, target, config); err != nil {
			return err
		}
	}
	for _, inbound := range listOfMaps(config["inbounds"]) {
		tag := stringValue(inbound["tag"])
		if strings.HasPrefix(tag, TSinLabPrefix) {
			if _, err := r.DeleteInbound(ctx, tag); err != nil && !errors.Is(err, ErrInboundNotFound) {
				return err
			}
		}
	}
	return nil
}

// TSinLabTests reads the lab on target back as links to address.
func (r Repository) TSinLabTests(ctx context.Context, target, address string) ([]TSinLabTest, error) {
	config, err := r.GetTargetRawConfig(ctx, target)
	if err != nil {
		return nil, err
	}
	host := address
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	var tests []TSinLabTest
	for _, recipe := range tsinLabRecipes() {
		tag := TSinLabPrefix + recipe.key
		var inbound map[string]any
		for _, candidate := range listOfMaps(config["inbounds"]) {
			if stringValue(candidate["tag"]) == tag {
				inbound = candidate
			}
		}
		if inbound == nil {
			continue
		}
		port, _ := parseConfigPort(inbound["port"])
		clients := listOfMaps(mapValue(inbound["settings"])["clients"])
		if len(clients) == 0 {
			continue
		}
		link, err := tsinLabLink(recipe.key, stringValue(clients[0]["id"]), host, port, mapValue(inbound["streamSettings"]), recipe.name)
		if err != nil {
			return nil, err
		}
		tests = append(tests, TSinLabTest{Tag: tag, Name: recipe.name, About: recipe.about, Port: port, Link: link})
	}
	return tests, nil
}

func tsinLabLink(key, id, host string, port int, stream map[string]any, name string) (string, error) {
	query := url.Values{"encryption": {"none"}}
	switch key {
	case "http":
		query.Set("type", "tcp")
		query.Set("security", "none")
		query.Set("headerType", "http")
		query.Set("host", "play.google.com")
		query.Set("path", "/")
	case "vmess-http":
		payload, err := json.Marshal(map[string]any{
			"v": "2", "ps": "LAB " + name, "add": strings.Trim(host, "[]"), "port": strconv.Itoa(port), "id": id,
			"aid": "0", "scy": "auto", "net": "tcp", "type": "http", "host": "play.google.com", "path": "/", "tls": "",
		})
		if err != nil {
			return "", err
		}
		return "vmess://" + base64.StdEncoding.EncodeToString(payload), nil
	case "xhttp-plain":
		query.Set("type", "xhttp")
		query.Set("security", "none")
		query.Set("path", stringValue(mapValue(stream["xhttpSettings"])["path"]))
		query.Set("mode", "auto")
	case "reality-gitlab", "reality-tcp":
		settings := mapValue(stream["realitySettings"])
		public, err := DeriveRealityPublicKey(stringValue(settings["privateKey"]))
		if err != nil {
			return "", err
		}
		query.Set("security", "reality")
		query.Set("pbk", public)
		query.Set("sid", stringValue(interfaceSlice(settings["shortIds"])[0]))
		query.Set("sni", stringValue(interfaceSlice(settings["serverNames"])[0]))
		query.Set("fp", "firefox")
		if key == "reality-gitlab" {
			query.Set("type", "xhttp")
			query.Set("path", stringValue(mapValue(stream["xhttpSettings"])["path"]))
			query.Set("mode", "auto")
		} else {
			query.Set("type", "tcp")
		}
	case "tls-xhttp":
		tls := mapValue(stream["tlsSettings"])
		pin, err := tsinLabCertPin(tls)
		if err != nil {
			return "", err
		}
		query.Set("type", "xhttp")
		query.Set("security", "tls")
		query.Set("sni", stringValue(tls["serverName"]))
		query.Set("fp", "firefox")
		query.Set("alpn", "h2,http/1.1")
		query.Set("path", stringValue(mapValue(stream["xhttpSettings"])["path"]))
		query.Set("mode", "auto")
		query.Set("pcs", pin)
		query.Set("allowInsecure", "1")
	default:
		return "", fmt.Errorf("unknown lab test %q", key)
	}
	return "vless://" + id + "@" + host + ":" + strconv.Itoa(port) + "?" + query.Encode() + "#" + url.PathEscape("LAB "+name), nil
}

// tsinLabCertPin is the SHA-256 of the inbound's certificate, which the link
// pins in place of a certificate authority.
func tsinLabCertPin(tls map[string]any) (string, error) {
	certificates := listOfMaps(tls["certificates"])
	if len(certificates) == 0 {
		return "", errors.New("the TLS test has no certificate")
	}
	var lines []string
	switch value := certificates[0]["certificate"].(type) {
	case []string:
		lines = value
	default:
		for _, line := range interfaceSlice(value) {
			lines = append(lines, stringValue(line))
		}
	}
	block, _ := pem.Decode([]byte(strings.Join(lines, "\n")))
	if block == nil {
		return "", errors.New("the TLS test's certificate does not read")
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}
