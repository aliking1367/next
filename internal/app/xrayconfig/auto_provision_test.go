//go:build cgo

package xrayconfig

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// useEmbeddedCatalogForTest forces loadCatalog to skip the network and use
// the binary's embedded default catalog, so tests are fast and deterministic
// regardless of network availability.
func useEmbeddedCatalogForTest(t *testing.T) {
	t.Helper()
	original := catalogFetcher
	catalogFetcher = func(context.Context) (Catalog, error) {
		return Catalog{}, errors.New("network disabled in tests")
	}
	catalogCacheMu.Lock()
	catalogCacheTime = time.Time{}
	catalogCacheMu.Unlock()
	t.Cleanup(func() {
		catalogFetcher = original
		catalogCacheMu.Lock()
		catalogCacheTime = time.Time{}
		catalogCacheMu.Unlock()
	})
}

// provisionTestRepository extends the minimal repository fixture with the
// columns and table that retiring inbounds and configuring a CDN host touch.
func provisionTestRepository(t *testing.T) (Repository, *sql.DB) {
	t.Helper()
	useEmbeddedCatalogForTest(t)
	repo, db := testRepository(t)
	for _, statement := range []string{
		`ALTER TABLE hosts ADD COLUMN sni TEXT NULL`,
		`ALTER TABLE hosts ADD COLUMN host TEXT NULL`,
		`ALTER TABLE hosts ADD COLUMN security TEXT NOT NULL DEFAULT 'inbound_default'`,
		`ALTER TABLE hosts ADD COLUMN alpn TEXT NOT NULL DEFAULT 'none'`,
		`ALTER TABLE hosts ADD COLUMN fingerprint TEXT NOT NULL DEFAULT 'none'`,
		`ALTER TABLE hosts ADD COLUMN pinned_peer_cert_sha256 TEXT NULL`,
		`CREATE TABLE service_hosts (service_id INTEGER, host_id INTEGER, sort INTEGER DEFAULT 0, created_at DATETIME NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	return repo, db
}

func protocolsByRecipe(result AutoProvisionResult) map[string]AutoProvisionProtocol {
	byRecipe := map[string]AutoProvisionProtocol{}
	for _, protocol := range result.Protocols {
		byRecipe[protocol.Recipe] = protocol
	}
	return byRecipe
}

func countRows(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return count
}

func TestAutoProvisionCreatesTheCurrentSetWithoutCDN(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()

	result, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatalf("AutoProvisionBestProtocols: %v", err)
	}
	if result.CDNRequested {
		t.Error("no CDN domain was supplied")
	}
	if len(result.Retired) != 0 {
		t.Errorf("nothing to retire on a fresh install, got %v", result.Retired)
	}
	byRecipe := protocolsByRecipe(result)
	if len(result.Protocols) != 3 || byRecipe[RecipeCDNXHTTP].Tag != "" {
		t.Fatalf("expected the three direct recipes and no CDN inbound, got %+v", result.Protocols)
	}
	if got := byRecipe[RecipeRealityVision]; got.Port != 443 || !got.Created || got.Protocol != "vless" {
		t.Errorf("the primary REALITY inbound belongs on 443, got %+v", got)
	}
	if got := byRecipe[RecipeRealityXHTTP]; got.Port != 8443 || !got.Created {
		t.Errorf("the XHTTP REALITY inbound prefers 8443, got %+v", got)
	}
	if got := byRecipe[RecipeHysteria2]; got.Protocol != "hysteria" || got.Port < autoInboundMinPort || got.Port > autoInboundMaxPort {
		t.Errorf("hysteria2 takes a random port, got %+v", got)
	}

	seenPorts := map[int]bool{}
	for _, protocol := range result.Protocols {
		if seenPorts[protocol.Port] {
			t.Errorf("port %d assigned twice", protocol.Port)
		}
		seenPorts[protocol.Port] = true
		if n := countRows(t, db, `SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, protocol.Tag); n != 1 {
			t.Errorf("%s: expected one host row, got %d", protocol.Tag, n)
		}
	}
}

func TestAutoProvisionInboundShapes(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	if _, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{}); err != nil {
		t.Fatal(err)
	}

	vision, err := repo.GetInbound(ctx, "auto-reality-vision")
	if err != nil {
		t.Fatal(err)
	}
	visionStream := mapValue(vision["streamSettings"])
	if streamNetwork(visionStream) != "tcp" || stringValue(visionStream["security"]) != "reality" {
		t.Errorf("vision inbound must be tcp+reality, got %v", visionStream)
	}
	sniffing := mapValue(vision["sniffing"])
	if !boolValue(sniffing["enabled"]) || !boolValue(sniffing["routeOnly"]) {
		t.Errorf("sniffing must be on and route-only, got %v", sniffing)
	}

	xhttp, err := repo.GetInbound(ctx, "auto-reality-xhttp")
	if err != nil {
		t.Fatal(err)
	}
	xhttpStream := mapValue(xhttp["streamSettings"])
	if streamNetwork(xhttpStream) != "xhttp" || stringValue(xhttpStream["security"]) != "reality" {
		t.Errorf("xhttp inbound must be xhttp+reality, got %v", xhttpStream)
	}
	if path := stringValue(mapValue(xhttpStream["xhttpSettings"])["path"]); !strings.HasPrefix(path, "/") || len(path) < 8 {
		t.Errorf("xhttp path must be a non-trivial absolute path, got %q", path)
	}

	// The two REALITY inbounds must not masquerade as the same site, and both
	// must use real, pooled targets.
	visionDest := stringValue(mapValue(visionStream["realitySettings"])["dest"])
	xhttpDest := stringValue(mapValue(xhttpStream["realitySettings"])["dest"])
	if visionDest == "" || xhttpDest == "" || visionDest == xhttpDest {
		t.Errorf("expected two different REALITY targets, got %q and %q", visionDest, xhttpDest)
	}

	hysteria, err := repo.GetInbound(ctx, "auto-hysteria2-obfs")
	if err != nil {
		t.Fatal(err)
	}
	masks := listOfMaps(mapValue(mapValue(hysteria["streamSettings"])["finalmask"])["udp"])
	if len(masks) != 1 {
		t.Fatalf("hysteria2 must carry one obfuscation mask, got %v", masks)
	}
	mask := masks[0]
	password := stringValue(mapValue(mask["settings"])["password"])
	if stringValue(mask["type"]) != "salamander" || len(password) < 16 {
		t.Errorf("expected a salamander mask with a strong password, got %v", mask)
	}
}

func TestAutoProvisionPinsTheHysteriaCertificateOnItsHost(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	result, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Protocols) != 3 {
		t.Fatalf("unexpected result %+v", result.Protocols)
	}

	// Without a pin a Hysteria2 client rejects the self-signed certificate, so
	// the host must carry the certificate's SHA-256. Compute the expectation
	// independently from the inbound's own PEM.
	inbound, err := repo.GetInbound(ctx, "auto-hysteria2-obfs")
	if err != nil {
		t.Fatal(err)
	}
	certificates := listOfMaps(mapValue(mapValue(inbound["streamSettings"])["tlsSettings"])["certificates"])
	if len(certificates) != 1 {
		t.Fatalf("expected one certificate, got %v", certificates)
	}
	var lines []string
	for _, item := range listAnyForTest(certificates[0]["certificate"]) {
		lines = append(lines, item)
	}
	block, _ := pem.Decode([]byte(strings.Join(lines, "\n")))
	if block == nil {
		t.Fatal("inbound certificate is not PEM")
	}
	digest := sha256.Sum256(block.Bytes)
	want := strings.ToUpper(hex.EncodeToString(digest[:]))

	var pin sql.NullString
	if err := db.QueryRow(`SELECT pinned_peer_cert_sha256 FROM hosts WHERE inbound_tag = 'auto-hysteria2-obfs'`).Scan(&pin); err != nil {
		t.Fatal(err)
	}
	if !pin.Valid || pin.String != want || len(want) != 64 {
		t.Errorf("hysteria2 host must pin the certificate: got %q want %q", pin.String, want)
	}
	// Certificate pins only make sense for the self-signed inbound.
	if n := countRows(t, db, `SELECT COUNT(*) FROM hosts WHERE inbound_tag <> 'auto-hysteria2-obfs' AND pinned_peer_cert_sha256 IS NOT NULL`); n != 0 {
		t.Errorf("only the hysteria2 host is pinned, %d others are", n)
	}
}

// listAnyForTest normalizes an inline certificate value to strings.
func listAnyForTest(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, stringValue(item))
		}
		return out
	}
	return nil
}

func TestAutoProvisionRerunIsIdempotent(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()
	first, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Protocols) != len(first.Protocols) {
		t.Fatalf("rerun changed the set: %+v vs %+v", first.Protocols, second.Protocols)
	}
	firstByTag := map[string]AutoProvisionProtocol{}
	for _, protocol := range first.Protocols {
		firstByTag[protocol.Tag] = protocol
	}
	for _, protocol := range second.Protocols {
		if protocol.Created {
			t.Errorf("%s was recreated on rerun", protocol.Tag)
		}
		if protocol.Port != firstByTag[protocol.Tag].Port {
			t.Errorf("%s changed port on rerun", protocol.Tag)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, protocol.Tag); n != 1 {
			t.Errorf("%s: expected one host row after rerun, got %d", protocol.Tag, n)
		}
	}
}

func TestAutoProvisionSkipsPreferredPortsThatAreTaken(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()

	// 443 is busy on this machine (e.g. the panel's own HTTPS), and 8443 is
	// already used by another inbound in the shared config.
	if _, err := repo.CreateInbound(ctx, map[string]any{
		"tag": "someone-elses", "protocol": "vless", "port": 8443,
		"settings": map[string]any{"clients": []any{}, "decryption": "none"},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{
		PortBusy: func(port int) bool { return port == 443 },
	})
	if err != nil {
		t.Fatal(err)
	}
	byRecipe := protocolsByRecipe(result)
	for _, recipe := range []string{RecipeRealityVision, RecipeRealityXHTTP} {
		port := byRecipe[recipe].Port
		if port == 443 || port == 8443 || port < autoInboundMinPort {
			t.Errorf("%s must fall back to a free random port, got %d", recipe, port)
		}
	}
}

func TestAutoProvisionRetiresLegacyInbounds(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()

	// Recreate what earlier releases produced.
	legacy := []map[string]any{
		{"tag": "auto-vless-reality", "protocol": "vless", "port": 30001, "settings": map[string]any{"clients": []any{}, "decryption": "none"}},
		{"tag": "auto-trojan-reality", "protocol": "trojan", "port": 30002, "settings": map[string]any{"clients": []any{}}},
		{"tag": "auto-shadowsocks", "protocol": "shadowsocks", "port": 30003, "settings": map[string]any{"clients": []any{}, "network": "tcp,udp"}},
	}
	for _, payload := range legacy {
		if _, err := repo.CreateInbound(ctx, payload); err != nil {
			t.Fatalf("seed %v: %v", payload["tag"], err)
		}
	}
	// A hand-made inbound must never be touched.
	if _, err := repo.CreateInbound(ctx, map[string]any{
		"tag": "my-own-vless", "protocol": "vless", "port": 30004,
		"settings": map[string]any{"clients": []any{}, "decryption": "none"},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.Retired, ",") != "auto-vless-reality,auto-trojan-reality,auto-shadowsocks" {
		t.Fatalf("expected the three seeded legacy inbounds to be retired in order, got %v", result.Retired)
	}
	for _, payload := range legacy {
		tag := payload["tag"].(string)
		if _, err := repo.GetInbound(ctx, tag); !errors.Is(err, ErrInboundNotFound) {
			t.Errorf("%s should be gone, got err=%v", tag, err)
		}
		if n := countRows(t, db, `SELECT COUNT(*) FROM hosts WHERE inbound_tag = ?`, tag); n != 0 {
			t.Errorf("%s: its host row must be removed with it, %d left", tag, n)
		}
	}
	if _, err := repo.GetInbound(ctx, "my-own-vless"); err != nil {
		t.Errorf("a hand-made inbound must survive: %v", err)
	}
	if len(result.Protocols) != 3 {
		t.Errorf("expected the current three inbounds, got %+v", result.Protocols)
	}

	// Retiring happens once: a further run has nothing left to retire.
	again, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Retired) != 0 {
		t.Errorf("nothing should be retired the second time, got %v", again.Retired)
	}
}

func TestAutoProvisionCDNInboundAndHost(t *testing.T) {
	repo, db := provisionTestRepository(t)
	ctx := context.Background()

	result, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{CDNDomain: "  Cdn.Example.COM. "})
	if err != nil {
		t.Fatal(err)
	}
	if !result.CDNRequested || len(result.Protocols) != 4 {
		t.Fatalf("expected four inbounds including the CDN one, got %+v", result)
	}
	cdn := protocolsByRecipe(result)[RecipeCDNXHTTP]
	validCDNPort := false
	for _, port := range cdnPreferredPorts {
		validCDNPort = validCDNPort || port == cdn.Port
	}
	if !validCDNPort {
		t.Errorf("Cloudflare only proxies specific HTTPS ports, got %d", cdn.Port)
	}

	inbound, err := repo.GetInbound(ctx, "auto-cdn-xhttp")
	if err != nil {
		t.Fatal(err)
	}
	stream := mapValue(inbound["streamSettings"])
	tlsSettings := mapValue(stream["tlsSettings"])
	xhttp := mapValue(stream["xhttpSettings"])
	if streamNetwork(stream) != "xhttp" || stringValue(stream["security"]) != "tls" {
		t.Errorf("CDN inbound must be xhttp+tls, got %v", stream)
	}
	if stringValue(tlsSettings["serverName"]) != "cdn.example.com" || stringValue(xhttp["host"]) != "cdn.example.com" {
		t.Errorf("domain must be normalized into the TLS name and Host header, got %v / %v", tlsSettings, xhttp)
	}
	if stringValue(xhttp["mode"]) != "packet-up" {
		t.Errorf("packet-up is the CDN-safe mode, got %q", stringValue(xhttp["mode"]))
	}

	var address, sni, host, security, alpn, fingerprint string
	if err := db.QueryRow(`SELECT address, sni, host, security, alpn, fingerprint FROM hosts WHERE inbound_tag = 'auto-cdn-xhttp'`).
		Scan(&address, &sni, &host, &security, &alpn, &fingerprint); err != nil {
		t.Fatal(err)
	}
	if address != "cdn.example.com" || sni != "cdn.example.com" || host != "cdn.example.com" ||
		security != "tls" || alpn != "h2" || fingerprint != "chrome" {
		t.Errorf("CDN host must dial the domain with a browser fingerprint, got %q %q %q %q %q %q",
			address, sni, host, security, alpn, fingerprint)
	}

	// Same domain: kept. Different domain: rebuilt for the new site.
	same, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{CDNDomain: "cdn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if protocolsByRecipe(same)[RecipeCDNXHTTP].Created {
		t.Error("the same domain must not rebuild the CDN inbound")
	}
	changed, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{CDNDomain: "other.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if !protocolsByRecipe(changed)[RecipeCDNXHTTP].Created {
		t.Error("a new domain must rebuild the CDN inbound")
	}
	if err := db.QueryRow(`SELECT address, sni FROM hosts WHERE inbound_tag = 'auto-cdn-xhttp'`).Scan(&address, &sni); err != nil {
		t.Fatal(err)
	}
	if address != "other.example.org" || sni != "other.example.org" {
		t.Errorf("host must follow the new domain, got %q / %q", address, sni)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM hosts WHERE inbound_tag = 'auto-cdn-xhttp'`); n != 1 {
		t.Errorf("expected exactly one CDN host after rebuilding, got %d", n)
	}

	// Leaving the domain out later does not delete an existing CDN inbound.
	without, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if protocolsByRecipe(without)[RecipeCDNXHTTP].Tag != "" {
		t.Error("without a domain the CDN inbound is simply not part of the run")
	}
	if _, err := repo.GetInbound(ctx, "auto-cdn-xhttp"); err != nil {
		t.Errorf("an existing CDN inbound must be left alone: %v", err)
	}
}

func TestNormalizeCDNDomain(t *testing.T) {
	valid := map[string]string{
		"":                       "",
		"   ":                    "",
		"cdn.example.com":        "cdn.example.com",
		"  CDN.Example.COM. ":    "cdn.example.com",
		"a-b.c1.example.co.uk":   "a-b.c1.example.co.uk",
		"xn--mnchen-3ya.example": "xn--mnchen-3ya.example",
	}
	for input, want := range valid {
		got, err := NormalizeCDNDomain(input)
		if err != nil || got != want {
			t.Errorf("NormalizeCDNDomain(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"1.2.3.4", "localhost", "https://cdn.example.com", "cdn.example.com:443",
		"cdn.example.com/path", "*.example.com", "-bad.example.com", "bad_.example.com",
		"exa mple.com", strings.Repeat("a", 64) + ".example.com",
	} {
		if got, err := NormalizeCDNDomain(input); err == nil {
			t.Errorf("NormalizeCDNDomain(%q) accepted %q", input, got)
		}
	}
}

func TestDefaultCatalogAvoidsTargetsKnownToGetBlocked(t *testing.T) {
	// Xray itself warns that Apple/iCloud targets "may get your IP blocked",
	// operators inside Iran reported Google and Speedtest blocked within days,
	// and CDN-hosted targets attract active probing.
	forbidden := []string{"apple.com", "icloud.com", "google.com", "speedtest.net", "cloudflare.com"}
	for _, option := range defaultCatalog().RealityPool {
		for _, bad := range forbidden {
			if strings.Contains(option.Dest, bad) {
				t.Errorf("catalog target %q is on the avoid list (%s)", option.Dest, bad)
			}
		}
	}
	if len(defaultCatalog().RealityPool) < 8 {
		t.Errorf("a small pool makes installs easy to fingerprint, got %d targets", len(defaultCatalog().RealityPool))
	}
}

// TestAutoProvisionedConfigIsAcceptedByRealXray runs the generated config
// through the real Xray binary's own validator. It is skipped unless
// NEXT_TEST_XRAY_BIN points at an xray executable, so CI without one stays
// green while a developer can prove Xray really accepts what we generate.
func TestAutoProvisionedConfigIsAcceptedByRealXray(t *testing.T) {
	xrayBin := os.Getenv("NEXT_TEST_XRAY_BIN")
	if xrayBin == "" {
		t.Skip("set NEXT_TEST_XRAY_BIN to an xray executable to validate against real Xray")
	}
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()
	if _, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{CDNDomain: "cdn.example.com", Gaming: true}); err != nil {
		t.Fatal(err)
	}
	config, err := repo.readMasterConfigForPlanning(ctx)
	if err != nil {
		t.Fatal(err)
	}
	config["outbounds"] = []any{map[string]any{"tag": "DIRECT", "protocol": "freedom"}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(xrayBin, "run", "-test", "-c", path).CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Configuration OK") {
		t.Fatalf("real Xray rejected the generated config: %v\n%s", err, output)
	}
	// Anything Xray flags as risky for the targets we choose is a regression.
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "Choosing") && strings.Contains(line, "as the target") {
			t.Errorf("Xray warns about a REALITY target: %s", line)
		}
	}
}

func TestGamingRecipesAreOptionalAndUDPBased(t *testing.T) {
	repo, _ := provisionTestRepository(t)
	ctx := context.Background()

	result, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range result.Protocols {
		if protocol.Recipe == RecipeGamingKCP {
			t.Fatalf("gaming inbounds must only appear when asked for: %#v", protocol)
		}
	}

	result, err = repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{Gaming: true})
	if err != nil {
		t.Fatal(err)
	}
	ports := map[int]string{}
	found := map[string]AutoProvisionProtocol{}
	for _, protocol := range result.Protocols {
		if previous, clash := ports[protocol.Port]; clash {
			t.Fatalf("port %d used by both %s and %s", protocol.Port, previous, protocol.Recipe)
		}
		ports[protocol.Port] = protocol.Recipe
		found[protocol.Recipe] = protocol
	}
	kcp, ok := found[RecipeGamingKCP]
	if !ok || !kcp.Created {
		t.Fatalf("mKCP inbound missing: %#v", result.Protocols)
	}
	inbound, err := repo.GetInbound(ctx, kcp.Tag)
	if err != nil {
		t.Fatal(err)
	}
	stream := mapValue(inbound["streamSettings"])
	if stringValue(stream["network"]) != "kcp" {
		t.Fatalf("gaming inbound must be mKCP, got %q", stream["network"])
	}
	kcpSettings := mapValue(stream["kcpSettings"])
	if intValue(kcpSettings["tti"]) != 10 || boolValue(kcpSettings["congestion"]) {
		t.Fatalf("latency settings lost: %#v", kcpSettings)
	}
	// Xray 26 removed mKCP's own seed/header; obfuscation lives in finalmask.
	if _, legacy := kcpSettings["seed"]; legacy {
		t.Fatal("mKCP seed was removed in Xray 26 and must not be written")
	}
	masks := listOfMaps(mapValue(stream["finalmask"])["udp"])
	if len(masks) != 1 || stringValue(masks[0]["type"]) != "mkcp-aes128gcm" {
		t.Fatalf("mKCP obfuscation missing: %#v", masks)
	}
	if key := stringValue(mapValue(masks[0]["settings"])["key"]); len(key) < 16 {
		t.Fatalf("obfuscation key looks weak: %q", key)
	}

	// Re-running keeps them instead of creating duplicates.
	again, err := repo.AutoProvisionBestProtocols(ctx, AutoProvisionOptions{Gaming: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range again.Protocols {
		if protocol.Created {
			t.Fatalf("re-run recreated %s", protocol.Tag)
		}
	}
}
