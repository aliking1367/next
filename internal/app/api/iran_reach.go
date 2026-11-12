package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The Iran reach watch checks, every few hours, whether the address users'
// direct configs point at (a front server, or the countries' server itself)
// still answers from inside Iran, and tells the admin on Telegram when it
// stops. It asks check-host.net, which has test servers in Iranian
// datacenters, for a TCP connection to one direct config's port. Those test
// servers are datacenters, not mobile operators, and the check only opens a
// TCP connection: it sees an address blocked nationwide, but not an operator
// that lets the port open and then cuts the connection itself (seen on
// Hamrah Aval with REALITY), so its wording claims only that the port opens.

// checkHostPollDelay is the wait between reading a check's results; tests
// shorten it.
var checkHostPollDelay = 5 * time.Second

const (
	iranReachEvery   = 3 * time.Hour
	iranReachFirst   = 3 * time.Minute
	checkHostBaseURL = "https://check-host.net"
)

// iranReachResult is the latest check: how many Iranian test servers reached
// the address.
type iranReachResult struct {
	Address   string    `json:"address"`
	Port      int       `json:"port"`
	Open      int       `json:"open"`
	Total     int       `json:"total"`
	CheckedAt time.Time `json:"checked_at"`
	Error     string    `json:"error,omitempty"`
}

func (r iranReachResult) blocked() bool { return r.Error == "" && r.Total >= 3 && r.Open == 0 }
func (r iranReachResult) healthy() bool { return r.Error == "" && r.Total > 0 && r.Open*2 >= r.Total }

type iranReachState struct {
	mu      sync.Mutex
	latest  *iranReachResult
	blocked bool
}

func (st *iranReachState) load() *iranReachResult {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.latest == nil {
		return nil
	}
	copied := *st.latest
	return &copied
}

func (s *Server) runIranReachWatch(ctx context.Context) {
	timer := time.NewTimer(iranReachFirst)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.checkIranReach(ctx)
			timer.Reset(iranReachEvery)
		}
	}
}

// iranReachTarget is the address and a port of the direct configs, or ok
// false when there are none to check.
func (s *Server) iranReachTarget(ctx context.Context, settings tsinSettings) (string, int, bool) {
	if settings.Transport != xrayconfig.TSinTransportPath || len(settings.DirectCountries) == 0 {
		return "", 0, false
	}
	address := strings.TrimSpace(settings.DirectAddress)
	if address == "" {
		address = s.tsinTargetAddress(ctx, settings.Target)
	}
	if address == "" {
		return "", 0, false
	}
	for _, name := range settings.DirectCountries {
		inbound, err := s.configRepo.GetInbound(ctx, xrayconfig.TSinDirectInboundTag(name))
		if err != nil {
			continue
		}
		if port, err := strconv.Atoi(fmt.Sprint(inbound["port"])); err == nil && port > 0 {
			return address, port, true
		}
	}
	return "", 0, false
}

func (s *Server) checkIranReach(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Warnf(logging.ComponentNode, "Iran reach check failed: %v", recovered)
		}
	}()
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	settings, err := s.loadTSinSettings(checkCtx)
	if err != nil {
		return
	}
	address, port, ok := s.iranReachTarget(checkCtx, settings)
	if !ok {
		return
	}
	result := iranReachResult{Address: address, Port: port, CheckedAt: time.Now()}
	result.Open, result.Total, err = checkHostTCPFromIran(checkCtx, http.DefaultClient, checkHostBaseURL, net.JoinHostPort(address, strconv.Itoa(port)))
	if err != nil {
		result.Error = err.Error()
	}

	st := &s.iranReach
	st.mu.Lock()
	wasBlocked := st.blocked
	st.latest = &result
	switch {
	case result.blocked():
		st.blocked = true
	case result.healthy():
		st.blocked = false
	}
	nowBlocked := st.blocked
	st.mu.Unlock()

	alert := func(title string, lines ...string) {
		s.telegramReports.TSinTunnel(context.WithoutCancel(ctx), title, lines...)
	}
	counts := fmt.Sprintf("از %d سرور تست در دیتاسنترهای ایران، %d سرور پورت را باز دیدند.", result.Total, result.Open)
	if nowBlocked && !wasBlocked {
		alert("پورت کانفیگ‌های ⚡ از داخل ایران بسته است",
			address+" پورت "+strconv.Itoa(port), counts,
			"احتمالاً در ایران فیلتر شده. اگر سرور جلویی است، آی‌پی‌اش را عوض کن یا سرور جدید بگیر و آی‌پی جدید را در پنجرهٔ Countries بگذار؛ کانفیگ‌های Cloudflare در این مدت کار می‌کنند.")
	} else if !nowBlocked && wasBlocked {
		alert("پورت کانفیگ‌های ⚡ دوباره از دیتاسنترهای ایران باز است", address+" پورت "+strconv.Itoa(port), counts,
			"این فقط یعنی پورت باز است؛ اپراتور موبایل ممکن است خود اتصال را ببندد. ⚡ را روی گوشی هم تست کن.")
	}
}

// checkHostTCPFromIran asks check-host.net's Iranian test servers to open a
// TCP connection to target and counts how many could.
func checkHostTCPFromIran(ctx context.Context, client *http.Client, base, target string) (open, total int, err error) {
	return checkHostFromIran(ctx, client, base, "check-tcp", target)
}

// checkHostHTTPFromIran asks the same servers to fetch an HTTPS address and
// counts how many got any HTTP answer: a domain whose name (SNI) is blocked
// gets none.
func checkHostHTTPFromIran(ctx context.Context, client *http.Client, base, target string) (open, total int, err error) {
	return checkHostFromIran(ctx, client, base, "check-http", target)
}

// checkHostReached reads one test server's result. A TCP check answers with
// an object that has "error" when it failed. An HTTP check answers with an
// array [ok, seconds, message, status code, address]; check-host sets ok to 0
// for any 5xx too, but a 5xx from Cloudflare still means the domain got
// through to Cloudflare, which is what is asked here, so any status code
// counts as reached.
func checkHostReached(result []any) bool {
	if len(result) == 0 || result[0] == nil {
		return false
	}
	switch first := result[0].(type) {
	case map[string]any:
		_, failed := first["error"]
		return !failed
	case []any:
		if len(first) == 0 {
			return false
		}
		if ok, _ := first[0].(float64); ok == 1 {
			return true
		}
		if len(first) > 3 {
			code, _ := first[3].(string)
			return strings.TrimSpace(code) != ""
		}
		return false
	}
	return false
}

func checkHostFromIran(ctx context.Context, client *http.Client, base, kind, target string) (open, total int, err error) {
	results, total, err := checkHostResultsFromIran(ctx, client, base, kind, target)
	for _, result := range results {
		if checkHostReached(result) {
			open++
		}
	}
	return open, total, err
}

// Iran's filtering lets a new Cloudflare domain answer a small request and
// then stalls the connection after a few tens of kilobytes, so a ping works
// and nothing loads. A fetch of the home page cannot see that; downloading a
// file of several hundred kilobytes can. Cloudflare serves cloudflareEdgeProbe
// itself, from its edge, on every domain it proxies, so the download is the
// same for every domain and needs nothing from the origin. The control host
// is a long-known Cloudflare domain: only test servers that download it in
// full count, so a test server that is slow or down today does not make a
// domain look filtered.
const (
	cloudflareEdgeProbe       = "/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1?ray=0123456789abcdef"
	cloudflareEdgeProbeHost   = "cdnjs.cloudflare.com"
	cloudflareDownloadMinimum = 3
)

// iranDownload is how a domain fared on the download from Iran: of Total
// test servers that downloaded the control in full, Complete downloaded it in
// full on this domain too and Reached got at least an answer.
type iranDownload struct {
	Reached  int
	Complete int
	Total    int
}

// throttled is a domain that at most a quarter of the test servers download
// through in full: the "ping but no speed" of a filtered new domain. A stalled
// download times out without a status, so it does not tell "answers but
// stalls" from "does not answer"; one datacenter network (Qom) is not
// filtered, which is why the bar is a quarter and not zero.
func (d iranDownload) throttled() bool { return iranSlow(d.Complete, d.Total) }

// checkHostDownloadComplete reads one test server's HTTP result: the download
// finished (check-host's ok) with status 200.
func checkHostDownloadComplete(result []any) bool {
	if len(result) == 0 {
		return false
	}
	first, ok := result[0].([]any)
	if !ok || len(first) < 4 {
		return false
	}
	done, _ := first[0].(float64)
	code, _ := first[3].(string)
	return done == 1 && strings.TrimSpace(code) == "200"
}

// checkCloudflareDownloadsFromIran downloads the edge probe through each
// domain, and through the control, from Iran's test servers.
func checkCloudflareDownloadsFromIran(ctx context.Context, client *http.Client, base string, domains []string) (map[string]iranDownload, error) {
	type answer struct {
		results map[string][]any
		err     error
	}
	hosts := append([]string{cloudflareEdgeProbeHost}, domains...)
	answers := make([]answer, len(hosts))
	var wg sync.WaitGroup
	for index, host := range hosts {
		wg.Add(1)
		go func(index int, host string) {
			defer wg.Done()
			results, _, err := checkHostResultsFromIran(ctx, client, base, "check-http", "https://"+host+cloudflareEdgeProbe)
			answers[index] = answer{results: results, err: err}
		}(index, host)
	}
	wg.Wait()
	if answers[0].err != nil {
		return nil, answers[0].err
	}
	usable := map[string]bool{}
	for node, result := range answers[0].results {
		if checkHostDownloadComplete(result) {
			usable[node] = true
		}
	}
	if len(usable) < cloudflareDownloadMinimum {
		return nil, fmt.Errorf("only %d of Iran's test servers could download from a known Cloudflare site right now, too few to judge", len(usable))
	}
	out := make(map[string]iranDownload, len(domains))
	for index, domain := range domains {
		item := answers[index+1]
		if item.err != nil {
			continue
		}
		result := iranDownload{Total: len(usable)}
		for node := range usable {
			if checkHostReached(item.results[node]) {
				result.Reached++
			}
			if checkHostDownloadComplete(item.results[node]) {
				result.Complete++
			}
		}
		out[domain] = result
	}
	return out, nil
}

// checkHostResultsFromIran runs one check-host check from every Iranian test
// server and returns each server's raw result (servers that never answered
// are missing) and how many servers were asked.
func checkHostResultsFromIran(ctx context.Context, client *http.Client, base, kind, target string) (map[string][]any, int, error) {
	getJSON := func(path string, out any) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("check-host answered HTTP %d", response.StatusCode)
		}
		return json.NewDecoder(response.Body).Decode(out)
	}

	var nodes struct {
		Nodes map[string]struct {
			Location []string `json:"location"`
		} `json:"nodes"`
	}
	if err := getJSON("/nodes/hosts", &nodes); err != nil {
		return nil, 0, err
	}
	query := url.Values{"host": {target}}
	for name, node := range nodes.Nodes {
		if len(node.Location) > 0 && strings.EqualFold(node.Location[0], "ir") {
			query.Add("node", name)
		}
	}
	if len(query["node"]) == 0 {
		return nil, 0, fmt.Errorf("check-host has no Iranian test servers right now")
	}
	var started struct {
		RequestID string `json:"request_id"`
	}
	if err := getJSON("/"+kind+"?"+query.Encode(), &started); err != nil {
		return nil, 0, err
	}
	if started.RequestID == "" {
		return nil, 0, fmt.Errorf("check-host did not start the check")
	}
	total := len(query["node"])
	// Results arrive over a few seconds (an HTTP check waits up to its own
	// timeout on a stalled download); a test server still null is waited for,
	// one that answered with an error counts as not reached.
	latest := map[string][]any{}
	for attempt := 0; attempt < 10; attempt++ {
		select {
		case <-ctx.Done():
			return latest, total, ctx.Err()
		case <-time.After(checkHostPollDelay):
		}
		var results map[string][]any
		if err := getJSON("/check-result/"+url.PathEscape(started.RequestID), &results); err != nil {
			continue
		}
		done := map[string][]any{}
		for node, result := range results {
			if result == nil {
				continue
			}
			done[node] = result
		}
		latest = done
		if len(done) >= total {
			break
		}
	}
	return latest, total, nil
}
