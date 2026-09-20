package xrayconfig

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
)

// Recipe identifiers, reported per inbound so the dashboard can label them.
const (
	RecipeRealityVision = "reality-vision"
	RecipeRealityXHTTP  = "reality-xhttp"
	RecipeHysteria2     = "hysteria2-obfs"
	RecipeCDNXHTTP      = "cdn-xhttp"
)

// autoProvisionLegacyTags are inbounds created by earlier releases of this
// feature. Xray's maintainers now flag Trojan and Shadowsocks as deprecated,
// and the earlier VLESS/Hysteria2 recipes lacked Vision-capable REALITY on
// 443, sniffing and obfuscation, so a re-run retires these and provisions the
// current set instead of leaving stale inbounds behind.
var autoProvisionLegacyTags = []string{
	"auto-vless-reality",
	"auto-trojan-reality",
	"auto-hysteria2",
	"auto-shadowsocks",
}

// Cloudflare only proxies HTTPS on a fixed set of ports; 443 is taken by the
// direct REALITY inbound, so the CDN inbound uses one of the others.
var cdnPreferredPorts = []int{2096, 2087, 2083, 2053, 8443}

// AutoProvisionOptions tunes one auto-provisioning run.
type AutoProvisionOptions struct {
	// CDNDomain, when set, also provisions a Cloudflare-fronted XHTTP+TLS
	// inbound. Clients then connect to Cloudflare's edge instead of the
	// origin IP, which keeps working when that IP is filtered. The domain
	// must already be proxied ("orange cloud") through Cloudflare.
	CDNDomain string
	// PortBusy reports whether a port is already taken on this machine by a
	// process outside Xray's config (e.g. the panel itself). Optional.
	PortBusy func(port int) bool
}

// AutoProvisionProtocol describes one inbound created (or already present)
// as part of an auto-provisioning run.
type AutoProvisionProtocol struct {
	Protocol string `json:"protocol"`
	Recipe   string `json:"recipe"`
	Tag      string `json:"tag"`
	Port     int    `json:"port"`
	Created  bool   `json:"created"`
}

// AutoProvisionResult is the outcome of AutoProvisionBestProtocols.
type AutoProvisionResult struct {
	Protocols []AutoProvisionProtocol `json:"protocols"`
	// Retired lists earlier-generation inbounds that were removed.
	Retired []string `json:"retired,omitempty"`
	// CDNRequested is true when a CDN domain was supplied.
	CDNRequested bool `json:"cdn_requested"`
}

type autoProvisionBuild struct {
	tag       string
	port      int
	catalog   Catalog
	usedDests map[string]bool
	cdnDomain string
}

type autoProvisionSpec struct {
	protocol       string
	recipe         string
	tag            string
	preferredPorts []int
	cdn            bool
	build          func(in autoProvisionBuild) (map[string]any, error)
}

func autoProvisionSpecs() []autoProvisionSpec {
	return []autoProvisionSpec{
		{protocol: "vless", recipe: RecipeRealityVision, tag: "auto-reality-vision", preferredPorts: []int{443}, build: buildAutoRealityVision},
		{protocol: "vless", recipe: RecipeRealityXHTTP, tag: "auto-reality-xhttp", preferredPorts: []int{8443}, build: buildAutoRealityXHTTP},
		{protocol: "hysteria", recipe: RecipeHysteria2, tag: "auto-hysteria2-obfs", build: buildAutoHysteria2},
		{protocol: "vless", recipe: RecipeCDNXHTTP, tag: "auto-cdn-xhttp", preferredPorts: cdnPreferredPorts, cdn: true, build: buildAutoCDNXHTTP},
	}
}

// AutoProvisionTags lists the inbound tags AutoProvisionBestProtocols owns,
// in the order they are provisioned. The CDN inbound only exists when a
// domain was supplied.
func AutoProvisionTags() []string {
	specs := autoProvisionSpecs()
	tags := make([]string, 0, len(specs))
	for _, spec := range specs {
		tags = append(tags, spec.tag)
	}
	return tags
}

// NormalizeCDNDomain validates a user-supplied domain for the CDN inbound and
// returns its canonical lower-case form. An empty value means "no CDN".
func NormalizeCDNDomain(value string) (string, error) {
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if domain == "" {
		return "", nil
	}
	if !isValidPublicHostname(domain) {
		return "", fmt.Errorf("%w: %q is not a valid domain name (use a plain domain such as sub.example.com, without scheme, port or IP address)", ErrInvalidInbound, value)
	}
	return domain, nil
}

// AutoProvisionBestProtocols provisions a curated, current set of Xray
// inbounds on the master's shared config, each getting its own auto-created
// host row exactly like a manually created inbound would
// (ensureInboundRecordTx):
//
//   - VLESS + REALITY (Vision-capable) on 443, the primary
//   - VLESS + REALITY + XHTTP, a different traffic shape for when the
//     primary is being blocked
//   - Hysteria2 with salamander obfuscation, a UDP option
//   - optionally VLESS + XHTTP + TLS behind Cloudflare, reachable even when
//     the server's own IP is filtered (needs a domain, see CDNDomain)
//
// Per-user credentials are generated by the existing user-creation flow.
// Re-running is safe: current inbounds are kept, earlier-generation ones are
// retired, and a CDN inbound is rebuilt only if its domain changed.
func (r Repository) AutoProvisionBestProtocols(ctx context.Context, opts AutoProvisionOptions) (AutoProvisionResult, error) {
	cdnDomain, err := NormalizeCDNDomain(opts.CDNDomain)
	if err != nil {
		return AutoProvisionResult{}, err
	}
	result := AutoProvisionResult{CDNRequested: cdnDomain != ""}

	for _, tag := range autoProvisionLegacyTags {
		if _, err := r.DeleteInbound(ctx, tag); err != nil {
			if errors.Is(err, ErrInboundNotFound) {
				continue
			}
			return AutoProvisionResult{}, fmt.Errorf("retire %s: %w", tag, err)
		}
		result.Retired = append(result.Retired, tag)
	}

	config, err := r.readMasterConfigForPlanning(ctx)
	if err != nil {
		return AutoProvisionResult{}, err
	}
	used, ranges := extractUsedPorts(config)
	catalog := r.loadCatalog(ctx)
	usedDests := map[string]bool{}

	for _, spec := range autoProvisionSpecs() {
		if spec.cdn && cdnDomain == "" {
			continue
		}
		existing, err := r.GetInbound(ctx, spec.tag)
		if err != nil && !errors.Is(err, ErrInboundNotFound) {
			return AutoProvisionResult{}, err
		}
		if existing != nil && spec.cdn && cdnInboundDomain(existing) != cdnDomain {
			// The domain changed: the old inbound's TLS name and host row would
			// point at the wrong site, so rebuild it.
			if _, err := r.DeleteInbound(ctx, spec.tag); err != nil {
				return AutoProvisionResult{}, fmt.Errorf("replace %s: %w", spec.tag, err)
			}
			existing = nil
		}
		if existing != nil {
			port, _ := parseConfigPort(existing["port"])
			result.Protocols = append(result.Protocols, AutoProvisionProtocol{
				Protocol: spec.protocol, Recipe: spec.recipe, Tag: spec.tag, Port: port, Created: false,
			})
			continue
		}

		port, err := pickProvisionPort(spec.preferredPorts, used, ranges, opts.PortBusy)
		if err != nil {
			return AutoProvisionResult{}, err
		}
		used[port] = true

		payload, err := spec.build(autoProvisionBuild{
			tag: spec.tag, port: port, catalog: catalog, usedDests: usedDests, cdnDomain: cdnDomain,
		})
		if err != nil {
			return AutoProvisionResult{}, fmt.Errorf("%s: %w", spec.recipe, err)
		}
		if _, err := r.CreateInbound(ctx, payload); err != nil {
			return AutoProvisionResult{}, fmt.Errorf("%s: %w", spec.recipe, err)
		}
		switch spec.recipe {
		case RecipeCDNXHTTP:
			if err := r.configureCDNHost(ctx, spec.tag, cdnDomain); err != nil {
				return AutoProvisionResult{}, fmt.Errorf("%s host: %w", spec.recipe, err)
			}
		case RecipeHysteria2:
			if err := r.pinHostCertificate(ctx, spec.tag, payload); err != nil {
				return AutoProvisionResult{}, fmt.Errorf("%s host: %w", spec.recipe, err)
			}
		}
		result.Protocols = append(result.Protocols, AutoProvisionProtocol{
			Protocol: spec.protocol, Recipe: spec.recipe, Tag: spec.tag, Port: port, Created: true,
		})
	}
	return result, nil
}

func cdnInboundDomain(inbound map[string]any) string {
	tlsSettings := mapValue(mapValue(inbound["streamSettings"])["tlsSettings"])
	return strings.ToLower(strings.TrimSpace(stringValue(tlsSettings["serverName"])))
}

// configureCDNHost points the CDN inbound's default host at the domain
// (clients dial Cloudflare, not the origin), with the TLS name, Host header
// and a browser fingerprint that Cloudflare-fronted traffic should present.
func (r Repository) configureCDNHost(ctx context.Context, tag, domain string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE hosts SET address = ?, sni = ?, host = ?, security = 'tls', alpn = 'h2', fingerprint = 'chrome' WHERE inbound_tag = ?`,
		domain, domain, domain, tag,
	)
	return err
}

// pinHostCertificate records the SHA-256 of the inbound's self-signed
// certificate on its host, so clients verify that exact certificate. Without
// a pin (or an insecure flag) a Hysteria2 client rejects a self-signed
// certificate, and pinning is safer than switching verification off.
func (r Repository) pinHostCertificate(ctx context.Context, tag string, inbound map[string]any) error {
	certificates := listOfMaps(mapValue(mapValue(inbound["streamSettings"])["tlsSettings"])["certificates"])
	if len(certificates) == 0 {
		return fmt.Errorf("inbound %q has no certificate to pin", tag)
	}
	pin, err := certificateSHA256(certificates[0]["certificate"])
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE hosts SET pinned_peer_cert_sha256 = ? WHERE inbound_tag = ?`, pin, tag)
	return err
}

// certificateSHA256 returns the upper-case hex SHA-256 of a PEM certificate
// given as inline lines — the same form the dashboard's "fetch fingerprint"
// action stores for a host.
func certificateSHA256(value any) (string, error) {
	var lines []string
	switch typed := value.(type) {
	case []string:
		lines = typed
	case []any:
		for _, item := range typed {
			lines = append(lines, stringValue(item))
		}
	default:
		return "", fmt.Errorf("certificate is not inline PEM")
	}
	block, _ := pem.Decode([]byte(strings.Join(lines, "\n")))
	if block == nil {
		return "", fmt.Errorf("certificate is not valid PEM")
	}
	digest := sha256.Sum256(block.Bytes)
	return strings.ToUpper(hex.EncodeToString(digest[:])), nil
}

func (r Repository) readMasterConfigForPlanning(ctx context.Context) (map[string]any, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollbackQuietly(tx)
	return r.masterRawConfigTx(ctx, tx)
}

// pickProvisionPort takes the first preferred port that is neither used by
// another inbound nor busy on this machine, and otherwise a random free one.
func pickProvisionPort(preferred []int, used map[int]bool, ranges []portRange, busy func(int) bool) (int, error) {
	for _, candidate := range preferred {
		if isPortUsed(candidate, used, ranges) {
			continue
		}
		if busy != nil && busy(candidate) {
			continue
		}
		return candidate, nil
	}
	return pickAvailablePortFrom(used, ranges)
}

func pickAvailablePortFrom(used map[int]bool, ranges []portRange) (int, error) {
	for i := 0; i < 200; i++ {
		candidate, err := randomPort(autoInboundMinPort, autoInboundMaxPort)
		if err != nil {
			break
		}
		if !isPortUsed(candidate, used, ranges) {
			return candidate, nil
		}
	}
	for candidate := autoInboundMinPort; candidate <= autoInboundMaxPort; candidate++ {
		if !isPortUsed(candidate, used, ranges) {
			return candidate, nil
		}
	}
	return 0, ErrNoAvailablePort
}

// autoProvisionSniffing lets Xray read the destination domain from
// HTTP/TLS/QUIC so routing rules (e.g. keeping Iranian domains direct) can
// apply. routeOnly leaves the real destination untouched.
func autoProvisionSniffing() map[string]any {
	return map[string]any{
		"enabled":      true,
		"destOverride": []any{"http", "tls", "quic"},
		"routeOnly":    true,
	}
}

func realitySettingsFor(catalog Catalog, usedDests map[string]bool) (map[string]any, error) {
	privateKey, _, err := generateRealityKeyPair()
	if err != nil {
		return nil, err
	}
	shortID, err := generateRealityShortIDHex()
	if err != nil {
		return nil, err
	}
	option := pickRealityDest(catalog.RealityPool, usedDests)
	usedDests[option.Dest] = true
	return map[string]any{
		"show":        false,
		"dest":        option.Dest,
		"xver":        0,
		"serverNames": stringsToAny(option.ServerNames),
		"privateKey":  privateKey,
		"shortIds":    []any{shortID},
	}, nil
}

// buildAutoRealityVision is the primary: VLESS over REALITY on TCP. Vision
// flow is applied per user through the bundle service (the panel drops it
// automatically on inbounds that cannot use it).
func buildAutoRealityVision(in autoProvisionBuild) (map[string]any, error) {
	reality, err := realitySettingsFor(in.catalog, in.usedDests)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      in.tag,
		"listen":   "::",
		"port":     in.port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network":         "tcp",
			"security":        "reality",
			"realitySettings": reality,
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// buildAutoRealityXHTTP is VLESS over REALITY with the XHTTP transport: an
// HTTP-shaped traffic pattern that operators inside Iran reported surviving
// blocks that hit plain REALITY streams.
func buildAutoRealityXHTTP(in autoProvisionBuild) (map[string]any, error) {
	reality, err := realitySettingsFor(in.catalog, in.usedDests)
	if err != nil {
		return nil, err
	}
	path, err := randomXHTTPPath()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      in.tag,
		"listen":   "::",
		"port":     in.port,
		"protocol": "vless",
		"settings": map[string]any{
			"clients":    []any{},
			"decryption": "none",
		},
		"streamSettings": map[string]any{
			"network":  "xhttp",
			"security": "reality",
			"xhttpSettings": map[string]any{
				"path": path,
				"mode": "auto",
			},
			"realitySettings": reality,
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// buildAutoHysteria2 is Hysteria2 (QUIC/UDP) with salamander obfuscation,
// which hides QUIC's recognizable packet structure.
func buildAutoHysteria2(in autoProvisionBuild) (map[string]any, error) {
	certLines, keyLines, err := generateSelfSignedCertLines("next-hysteria2")
	if err != nil {
		return nil, err
	}
	obfsPassword, err := randomToken(16)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      in.tag,
		"listen":   "::",
		"port":     in.port,
		"protocol": "hysteria",
		"settings": map[string]any{
			"clients": []any{},
		},
		"streamSettings": map[string]any{
			"tlsSettings": map[string]any{
				"alpn": []any{"h3"},
				"certificates": []any{
					map[string]any{
						"certificate": certLines,
						"key":         keyLines,
					},
				},
			},
			"finalmask": map[string]any{
				"udp": []any{
					map[string]any{
						"type":     "salamander",
						"settings": map[string]any{"password": obfsPassword},
					},
				},
			},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

// buildAutoCDNXHTTP is VLESS over XHTTP+TLS meant to sit behind Cloudflare.
// The origin certificate is self-signed (Cloudflare's SSL mode must be
// "Full"); clients see Cloudflare's own valid certificate. packet-up is the
// XHTTP mode reported to work through every CDN.
func buildAutoCDNXHTTP(in autoProvisionBuild) (map[string]any, error) {
	certLines, keyLines, err := generateSelfSignedCertLines(in.cdnDomain, in.cdnDomain)
	if err != nil {
		return nil, err
	}
	path, err := randomXHTTPPath()
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"tag":      in.tag,
		"listen":   "::",
		"port":     in.port,
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
				"host": in.cdnDomain,
				"mode": "packet-up",
			},
			"tlsSettings": map[string]any{
				"serverName": in.cdnDomain,
				"alpn":       []any{"h2", "http/1.1"},
				"minVersion": "1.2",
				"certificates": []any{
					map[string]any{
						"certificate": certLines,
						"key":         keyLines,
					},
				},
			},
		},
		"sniffing": autoProvisionSniffing(),
	}, nil
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func randomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func randomXHTTPPath() (string, error) {
	token, err := randomToken(6)
	if err != nil {
		return "", err
	}
	return "/" + token, nil
}

func generateRealityKeyPair() (privateKey string, publicKey string, err error) {
	priv := make([]byte, curve25519.ScalarSize)
	if _, err = rand.Read(priv); err != nil {
		return "", "", err
	}
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(priv), base64.RawURLEncoding.EncodeToString(pub), nil
}

func generateRealityShortIDHex() (string, error) {
	value := make([]byte, 4)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// generateSelfSignedCertLines creates a fresh self-signed leaf certificate so
// protocols that require TLS (Hysteria2) work immediately from just a server
// IP, with no real domain/CA involved. Clients connect with certificate
// verification disabled for this host, same as any self-signed TLS setup.
func generateSelfSignedCertLines(commonName string, dnsNames ...string) ([]string, []string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		DNSNames:              dnsNames,
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})
	return splitPEMLines(string(certPEM)), splitPEMLines(string(keyPEM)), nil
}

func splitPEMLines(value string) []string {
	trimmed := strings.TrimRight(value, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
