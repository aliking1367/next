// Package tsin talks to a T.Sin Pro account and keeps the WireGuard tunnel its
// SOCKS exits live behind.
//
// T.Sin sells exit IPs by country. An account is a WireGuard peer into the
// provider's network; inside it, every country is a SOCKS proxy on
// 10.0.0.1:<port>. Xray reaches a country by using that proxy as an outbound.
//
// Two things the provider hands back are treated as untrusted. The WireGuard
// profile is rebuilt from validated fields instead of being written as
// received, because wg-quick runs PostUp/PreUp lines as root and honours
// AllowedIPs = 0.0.0.0/0, which would send the whole server through the
// tunnel and cut off its own SSH. And the API is plain HTTP with the token in
// the query string, which is how the service is built; callers should tell the
// admin so.
package tsin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Interface is the WireGuard interface the panel owns. It is deliberately not
// wg0, which the provider's own installer uses, so the two never collide.
const Interface = "tsin0"

const maxBody = 1 << 20

// Preferred are the countries offered first: the ones whose exit addresses
// are accepted by the AI services users most often ask for.
var Preferred = []string{
	"Germany", "Turkey", "United Kingdom", "France dc", "The Netherlands",
	"United States", "Canada", "Switzerland", "Sweden",
}

var dcPattern = regexp.MustCompile(`^dc[1-5]$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{6,200}$`)

// Datacenters are the API hosts the provider operates.
func Datacenters() []string { return []string{"dc1", "dc2", "dc3", "dc4", "dc5"} }

func ValidDC(dc string) bool { return dcPattern.MatchString(dc) }

func ValidToken(token string) bool { return tokenPattern.MatchString(token) }

type Client struct {
	HTTP *http.Client
	// BaseURL overrides the provider address; tests point it at a local server.
	BaseURL func(dc string) string
}

func NewClient() Client {
	return Client{HTTP: &http.Client{Timeout: 25 * time.Second}}
}

func (c Client) base(dc string) string {
	if c.BaseURL != nil {
		return c.BaseURL(dc)
	}
	return "http://" + dc + ".sin-kabir.ir:8080"
}

func (c Client) get(ctx context.Context, dc, path, token string, out any) error {
	if !ValidDC(dc) {
		return fmt.Errorf("unknown T.Sin datacenter %q", dc)
	}
	if !ValidToken(token) {
		return errors.New("the T.Sin token has an unexpected format")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base(dc)+path+"?token="+url.QueryEscape(token), nil)
	if err != nil {
		return err
	}
	client := c.HTTP
	if client == nil {
		client = NewClient().HTTP
	}
	response, err := client.Do(request)
	if err != nil {
		// The URL carries the token, so it must not reach an error message.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("T.Sin %s unreachable: %w", dc, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("T.Sin %s answered with something that is not JSON (HTTP %d)", dc, response.StatusCode)
	}
	return nil
}

type RegisterResult struct {
	Status     string `json:"status"`
	Message    string `json:"message"`
	ClientName string `json:"client_name"`
	WGConf     string `json:"wg_conf"`
	ServerWGIP string `json:"server_wg_ip"`
}

func (c Client) Register(ctx context.Context, dc, token string) (RegisterResult, error) {
	var result RegisterResult
	if err := c.get(ctx, dc, "/register", token, &result); err != nil {
		return result, err
	}
	if result.Status != "success" {
		return result, fmt.Errorf("T.Sin refused the registration: %s", firstNonEmpty(result.Message, result.Status, "no reason given"))
	}
	return result, nil
}

type Proxy struct {
	Port    int    `json:"port"`
	Country string `json:"country"`
	Mode    string `json:"mode"`
	PingMS  string `json:"ping_ms"`
}

type Status struct {
	ClientName        string  `json:"client_name"`
	DataUsedGB        float64 `json:"data_used_gb"`
	DataLimitGB       float64 `json:"data_limit_gb"`
	ActiveConnections int     `json:"active_connections"`
	ExpiryDate        string  `json:"expiry_date"`
	Active            bool    `json:"active"`
	Proxies           []Proxy `json:"proxies"`
}

func (c Client) Status(ctx context.Context, dc, token string) (Status, error) {
	var raw struct {
		Status            string          `json:"status"`
		Message           string          `json:"message"`
		ClientName        string          `json:"client_name"`
		DataUsedGB        float64         `json:"data_used_gb"`
		DataLimitGB       float64         `json:"data_limit_gb"`
		ActiveConnections float64         `json:"active_connections"`
		ExpiryDate        string          `json:"expiry_date"`
		IsActive          float64         `json:"is_active"`
		ActiveProxies     map[string]rawP `json:"active_proxies"`
	}
	if err := c.get(ctx, dc, "/status", token, &raw); err != nil {
		return Status{}, err
	}
	if raw.Status != "success" {
		return Status{}, fmt.Errorf("T.Sin refused the token: %s", firstNonEmpty(raw.Message, raw.Status, "no reason given"))
	}
	status := Status{
		ClientName:        raw.ClientName,
		DataUsedGB:        raw.DataUsedGB,
		DataLimitGB:       raw.DataLimitGB,
		ActiveConnections: int(raw.ActiveConnections),
		ExpiryDate:        raw.ExpiryDate,
		Active:            raw.IsActive != 0,
	}
	for key, proxy := range raw.ActiveProxies {
		port, err := strconv.Atoi(key)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		status.Proxies = append(status.Proxies, Proxy{
			Port: port, Country: strings.TrimSpace(proxy.Country), Mode: proxy.Mode, PingMS: fmt.Sprint(proxy.PingMS),
		})
	}
	sort.Slice(status.Proxies, func(i, j int) bool { return status.Proxies[i].Country < status.Proxies[j].Country })
	return status, nil
}

type rawP struct {
	Country string `json:"country"`
	Mode    string `json:"mode"`
	PingMS  any    `json:"ping_ms"`
}

// Resolve maps country names to the proxy ports of this account.
func (s Status) Resolve(names []string) ([]Proxy, []string) {
	byName := map[string]Proxy{}
	for _, proxy := range s.Proxies {
		byName[strings.ToLower(proxy.Country)] = proxy
	}
	var found []Proxy
	var missing []string
	seen := map[int]bool{}
	for _, name := range names {
		proxy, ok := byName[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if !seen[proxy.Port] {
			seen[proxy.Port] = true
			found = append(found, proxy)
		}
	}
	return found, missing
}

// BuildConfig turns the provider's profile into one the panel is willing to
// run. Only the fields a tunnel needs survive, and the peer may only own the
// provider's single proxy address.
func BuildConfig(reg RegisterResult) (string, error) {
	fields := map[string]string{}
	section := ""
	for _, line := range strings.Split(reg.WGConf, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			section = strings.ToLower(strings.Trim(line, "[] "))
		default:
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				return "", errors.New("the WireGuard profile has a line that is not key = value")
			}
			fields[section+"."+strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}

	privateKey := fields["interface.privatekey"]
	publicKey := fields["peer.publickey"]
	if !validKey(privateKey) || !validKey(publicKey) {
		return "", errors.New("the WireGuard profile has an invalid key")
	}
	address, err := netip.ParsePrefix(fields["interface.address"])
	if err != nil || !address.Addr().Is4() || !address.Addr().IsPrivate() {
		return "", errors.New("the WireGuard profile has an unexpected interface address")
	}
	host, port, err := net.SplitHostPort(fields["peer.endpoint"])
	if err != nil {
		return "", errors.New("the WireGuard profile has an invalid endpoint")
	}
	endpointIP, err := netip.ParseAddr(host)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || !endpointIP.Is4() {
		return "", errors.New("the WireGuard profile has an invalid endpoint")
	}
	allowed, err := netip.ParsePrefix(fields["peer.allowedips"])
	if err != nil || !allowed.Addr().Is4() || allowed.Bits() != 32 || !allowed.Addr().IsPrivate() {
		return "", errors.New("the WireGuard profile would route more than the provider's proxy address; refusing to apply it")
	}
	if reg.ServerWGIP != "" && reg.ServerWGIP != allowed.Addr().String() {
		return "", errors.New("the WireGuard profile does not match the proxy address the provider reported")
	}

	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s/32\n\n[Peer]\nPublicKey = %s\nEndpoint = %s\nAllowedIPs = %s\nPersistentKeepalive = 25\n",
		privateKey, address.Addr(), publicKey, net.JoinHostPort(endpointIP.String(), port), allowed), nil
}

// ProxyAddress is where the country proxies listen inside the tunnel.
func ProxyAddress(conf string) string {
	for _, line := range strings.Split(conf, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "AllowedIPs") {
			if prefix, err := netip.ParsePrefix(strings.TrimSpace(value)); err == nil {
				return prefix.Addr().String()
			}
		}
	}
	return "10.0.0.1"
}

func validKey(value string) bool {
	raw, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(raw) == 32
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Tunnel owns the interface on this host.
type Tunnel struct {
	Dir string
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Installed reports whether wg-quick exists; tests replace it.
	Installed func() bool
}

func NewTunnel() Tunnel {
	return Tunnel{Dir: "/etc/wireguard", Run: runCommand, Installed: func() bool {
		_, err := exec.LookPath("wg-quick")
		return err == nil
	}}
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (t Tunnel) confPath() string { return filepath.Join(t.Dir, Interface+".conf") }

// ConfigExists reports whether a profile is already on disk.
func (t Tunnel) ConfigExists() bool {
	_, err := os.Stat(t.confPath())
	return err == nil
}

// ReadConfig returns the stored profile.
func (t Tunnel) ReadConfig() (string, error) {
	data, err := os.ReadFile(t.confPath())
	return string(data), err
}

// Up writes the profile and brings the interface up, now and at every boot.
func (t Tunnel) Up(ctx context.Context, conf string) error {
	if !t.Installed() {
		if out, err := t.Run(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "wireguard-tools"); err != nil {
			return fmt.Errorf("wireguard-tools is missing and could not be installed: %s", tail(out, err))
		}
	}
	if err := os.MkdirAll(t.Dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(t.confPath(), []byte(conf), 0o600); err != nil {
		return err
	}
	// An interface brought up by hand earlier would make the unit fail to start.
	_, _ = t.Run(ctx, "wg-quick", "down", Interface)
	_, _ = t.Run(ctx, "systemctl", "enable", "wg-quick@"+Interface)
	if out, err := t.Run(ctx, "systemctl", "restart", "wg-quick@"+Interface); err != nil {
		return fmt.Errorf("could not start the %s tunnel: %s", Interface, tail(out, err))
	}
	return nil
}

// Down removes the interface and its profile.
func (t Tunnel) Down(ctx context.Context) error {
	_, _ = t.Run(ctx, "systemctl", "disable", "--now", "wg-quick@"+Interface)
	_, _ = t.Run(ctx, "wg-quick", "down", Interface)
	if err := os.Remove(t.confPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Handshake is the time of the last completed WireGuard handshake.
func (t Tunnel) Handshake(ctx context.Context) (time.Time, bool) {
	out, err := t.Run(ctx, "wg", "show", Interface, "latest-handshakes")
	if err != nil {
		return time.Time{}, false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return time.Time{}, false
	}
	seconds, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

// WaitHandshake polls for a fresh handshake after the interface came up.
func (t Tunnel) WaitHandshake(ctx context.Context, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		if when, ok := t.Handshake(ctx); ok && time.Since(when) < 3*time.Minute {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

func tail(out []byte, err error) string {
	text := strings.TrimSpace(string(out))
	if len(text) > 300 {
		text = text[len(text)-300:]
	}
	if text == "" {
		return err.Error()
	}
	return text
}

// NodeScript is what an admin runs on a node to bring the tunnel up there.
//
// The panel cannot run commands on a node, and the tunnel has to exist on the
// machine whose Xray uses it. The script asks for the token itself, so the
// token is typed on the node and is never part of the text the panel shows.
// It rebuilds the WireGuard profile from validated fields for the same reason
// BuildConfig does: wg-quick runs PostUp as root and honours a default route.
func NodeScript(dc string) string {
	if !ValidDC(dc) {
		dc = "dc1"
	}
	script := strings.ReplaceAll(nodeScriptTemplate, "@DC@", dc)
	// Pasted straight into a shell, "set -e" in the script would apply to the
	// admin's own login shell and close the SSH session at the first failed
	// command. Wrapping it in a file keeps the script's strictness inside the
	// script.
	return "sudo tee /tmp/tsin-node.sh >/dev/null <<'TSIN_SCRIPT_END'\n" + script +
		"TSIN_SCRIPT_END\nsudo bash /tmp/tsin-node.sh; sudo rm -f /tmp/tsin-node.sh\n"
}

const nodeScriptTemplate = `#!/bin/bash
# Brings up the T.Sin tunnel (interface tsin0) on this server. Run as root.
set -euo pipefail
DC=@DC@
read -rs -p "T.Sin token: " TOKEN </dev/tty; echo
if [[ ! "$TOKEN" =~ ^[A-Za-z0-9._~-]{6,200}$ ]]; then echo "That does not look like a T.Sin token"; exit 1; fi
if ! command -v wg-quick >/dev/null || ! command -v python3 >/dev/null; then
  apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard-tools python3 >/dev/null
fi
REG=$(mktemp); trap 'shred -u "$REG" 2>/dev/null || rm -f "$REG"' EXIT
curl -fsS -m 25 "http://$DC.sin-kabir.ir:8080/register?token=$TOKEN" -o "$REG"
unset TOKEN
REG="$REG" python3 - <<'PY'
import os, json, base64, ipaddress, sys
d = json.load(open(os.environ["REG"]))
if d.get("status") != "success":
    sys.exit("T.Sin refused the registration: " + str(d.get("message", d.get("status"))))
f, sec = {}, ""
for line in d["wg_conf"].splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    if line.startswith("["):
        sec = line.strip("[] ").lower()
        continue
    k, _, v = line.partition("=")
    f[sec + "." + k.strip().lower()] = v.strip()
def key(v):
    if len(base64.b64decode(v, validate=True)) != 32:
        raise ValueError("bad key")
    return v
priv, pub = key(f["interface.privatekey"]), key(f["peer.publickey"])
addr = ipaddress.ip_interface(f["interface.address"])
assert addr.ip.version == 4 and addr.ip.is_private, "unexpected interface address"
host, _, port = f["peer.endpoint"].rpartition(":")
ep = ipaddress.ip_address(host)
assert ep.version == 4 and 0 < int(port) < 65536, "unexpected endpoint"
al = ipaddress.ip_network(f["peer.allowedips"], strict=True)
assert al.version == 4 and al.prefixlen == 32 and al.network_address.is_private, "profile would route more than the proxy address"
os.umask(0o077)
os.makedirs("/etc/wireguard", exist_ok=True)
open("/etc/wireguard/tsin0.conf", "w").write(
    "[Interface]\nPrivateKey = %s\nAddress = %s/32\n\n[Peer]\nPublicKey = %s\nEndpoint = %s:%s\nAllowedIPs = %s\nPersistentKeepalive = 25\n"
    % (priv, addr.ip, pub, ep, port, al))
print("profile written")
PY
wg-quick down tsin0 2>/dev/null || true
systemctl enable wg-quick@tsin0 >/dev/null 2>&1 || true
systemctl restart wg-quick@tsin0
for i in 1 2 3 4 5 6 7 8 9 10; do
  HS=$(wg show tsin0 latest-handshakes | awk '{print $2}')
  if [ -n "$HS" ] && [ "$HS" != "0" ]; then echo "Tunnel is up (handshake completed)"; exit 0; fi
  sleep 1
done
echo "No handshake yet: check that outbound UDP to the provider is not blocked on this server"; exit 1
`
