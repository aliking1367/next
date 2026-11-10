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
// servers are datacenters, not mobile operators, so this sees a nationwide
// block but not one on a single operator.

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
	counts := fmt.Sprintf("از %d سرور تست داخل ایران، %d سرور وصل شد.", result.Total, result.Open)
	if nowBlocked && !wasBlocked {
		alert("آی‌پی کانفیگ‌های ⚡ از داخل ایران در دسترس نیست",
			address+" پورت "+strconv.Itoa(port), counts,
			"احتمالاً در ایران فیلتر شده. اگر سرور جلویی است، آی‌پی‌اش را عوض کن یا سرور جدید بگیر و آی‌پی جدید را در پنجرهٔ Countries بگذار؛ کانفیگ‌های Cloudflare در این مدت کار می‌کنند.")
	} else if !nowBlocked && wasBlocked {
		alert("آی‌پی کانفیگ‌های ⚡ دوباره از ایران در دسترس است", address+" پورت "+strconv.Itoa(port), counts)
	}
}

// checkHostTCPFromIran asks check-host.net's Iranian test servers to open a
// TCP connection to target and counts how many could.
func checkHostTCPFromIran(ctx context.Context, client *http.Client, base, target string) (open, total int, err error) {
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
		return 0, 0, err
	}
	query := url.Values{"host": {target}}
	for name, node := range nodes.Nodes {
		if len(node.Location) > 0 && strings.EqualFold(node.Location[0], "ir") {
			query.Add("node", name)
		}
	}
	if len(query["node"]) == 0 {
		return 0, 0, fmt.Errorf("check-host has no Iranian test servers right now")
	}
	var started struct {
		RequestID string `json:"request_id"`
	}
	if err := getJSON("/check-tcp?"+query.Encode(), &started); err != nil {
		return 0, 0, err
	}
	if started.RequestID == "" {
		return 0, 0, fmt.Errorf("check-host did not start the check")
	}
	total = len(query["node"])
	// Results arrive over a few seconds; a test server still null is waited
	// for, one that answered with an error counts as not reached.
	for attempt := 0; attempt < 8; attempt++ {
		select {
		case <-ctx.Done():
			return 0, total, ctx.Err()
		case <-time.After(checkHostPollDelay):
		}
		var results map[string][]map[string]any
		if err := getJSON("/check-result/"+url.PathEscape(started.RequestID), &results); err != nil {
			continue
		}
		done, reached := 0, 0
		for _, result := range results {
			if result == nil {
				continue
			}
			done++
			if len(result) > 0 && result[0] != nil {
				if _, failed := result[0]["error"]; !failed {
					reached++
				}
			}
		}
		if done >= total {
			return reached, total, nil
		}
		open = reached
	}
	return open, total, nil
}
