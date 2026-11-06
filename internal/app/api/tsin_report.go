package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliking1367/next/internal/app/tsin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// When the countries are served from a node, the tunnel lives there and T.Sin
// shows the account only to the server holding the session; the panel itself
// is refused as "in use". So the node script reports to the panel every
// minute: the tunnel, the account as T.Sin shows it to the node, a test of
// each country's proxy, and the watch's log. Reports are kept in memory only;
// the next one arrives within a minute of a restart.

// tsinReportFresh is how old a report may be and still describe the node.
const tsinReportFresh = 5 * time.Minute

type tsinNodeReport struct {
	Version      int             `json:"version"`
	DC           string          `json:"dc"`
	Failures     int             `json:"failures"`
	HandshakeAge int64           `json:"handshake_age"`
	RX           int64           `json:"rx"`
	TX           int64           `json:"tx"`
	Account      json.RawMessage `json:"account,omitempty"`
	AccountError string          `json:"account_error,omitempty"`
	Ports        map[string]bool `json:"ports,omitempty"`
	PortsAt      int64           `json:"ports_at,omitempty"`
	Log          []string        `json:"log,omitempty"`

	// Target is the node the report was accepted for; ReceivedAt is when.
	Target     string    `json:"-"`
	ReceivedAt time.Time `json:"-"`
}

// tsinNodeState holds the latest node report.
type tsinNodeState struct {
	mu     sync.Mutex
	latest *tsinNodeReport
}

func (st *tsinNodeState) store(report *tsinNodeReport) {
	st.mu.Lock()
	st.latest = report
	st.mu.Unlock()
}

// load returns the latest report for target, fresh or not; nil if none.
func (st *tsinNodeState) load(target string) *tsinNodeReport {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.latest == nil || st.latest.Target != target {
		return nil
	}
	copied := *st.latest
	return &copied
}

func (r *tsinNodeReport) fresh() bool {
	return r != nil && time.Since(r.ReceivedAt) < tsinReportFresh
}

func (r *tsinNodeReport) tunnelUp() bool {
	return r.HandshakeAge >= 0 && r.HandshakeAge < int64(tsinHandshakeFresh/time.Second)
}

// countedPorts is the node's last test of the countries in use: the node
// tests every country of the account, but one nobody was given is no
// concern. Without the account to tell ports apart, every port counts.
func (r *tsinNodeReport) countedPorts(status *tsin.Status, inUse []string) map[string]bool {
	if status == nil || len(inUse) == 0 {
		return r.Ports
	}
	wanted := map[string]bool{}
	for _, name := range inUse {
		wanted[strings.ToLower(strings.TrimSpace(name))] = true
	}
	counted := map[string]bool{}
	for _, proxy := range status.Proxies {
		port := strconv.Itoa(proxy.Port)
		if ok, tested := r.Ports[port]; tested && wanted[strings.ToLower(strings.TrimSpace(proxy.Country))] {
			counted[port] = ok
		}
	}
	return counted
}

// failingPorts lists the countries in use whose proxy failed the node's last
// test, by name where the account names them.
func (r *tsinNodeReport) failingPorts(status *tsin.Status, inUse []string) []string {
	names := map[string]string{}
	if status != nil {
		for _, proxy := range status.Proxies {
			names[strconv.Itoa(proxy.Port)] = proxy.Country
		}
	}
	var failing []string
	for port, ok := range r.countedPorts(status, inUse) {
		if ok {
			continue
		}
		if name := strings.TrimSpace(names[port]); name != "" {
			failing = append(failing, name+" ("+port+")")
		} else {
			failing = append(failing, port)
		}
	}
	sort.Strings(failing)
	return failing
}

// tsinReportKey signs a node's reports. It is derived from the panel's own
// secret, so it needs no storage and changes only with that secret.
func (s *Server) tsinReportKey(ctx context.Context) string {
	secret, err := s.nodeSessionCallbackSecret(ctx)
	if err != nil || strings.TrimSpace(secret) == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("next-tsin-node-report"))
	return hex.EncodeToString(mac.Sum(nil))
}

// handleTSinNodeReport takes a node's report. The node holds no admin
// session, so the signed key is its only credential.
func (s *Server) handleTSinNodeReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	key := s.tsinReportKey(r.Context())
	if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(bearerToken(r))) != 1 {
		writeError(w, http.StatusForbidden, "invalid report key")
		return
	}
	var report tsinNodeReport
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "invalid report")
		return
	}
	settings, err := s.loadTSinSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if settings.Target == xrayconfig.MasterTargetID {
		// The panel watches its own tunnel; a stray report changes nothing.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
		return
	}
	if !tsin.ValidDC(report.DC) {
		report.DC = ""
	}
	if len(report.Log) > 10 {
		report.Log = report.Log[len(report.Log)-10:]
	}
	for i, line := range report.Log {
		if len(line) > 300 {
			report.Log[i] = line[:300]
		}
	}
	if len(report.AccountError) > 400 {
		report.AccountError = report.AccountError[:400]
	}
	if len(report.Ports) > 64 {
		report.Ports = nil
	}
	report.Target, report.ReceivedAt = settings.Target, time.Now()
	s.tsinNode.store(&report)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// tsinNodeView is what the dashboard gets from a node report.
func tsinNodeView(report *tsinNodeReport, status *tsin.Status, inUse []string) map[string]any {
	view := map[string]any{
		"age_seconds":   int64(time.Since(report.ReceivedAt).Seconds()),
		"fresh":         report.fresh(),
		"dc":            report.DC,
		"tunnel_up":     report.tunnelUp(),
		"handshake_age": report.HandshakeAge,
		"failures":      report.Failures,
		"rx":            report.RX,
		"tx":            report.TX,
		"log":           report.Log,
	}
	if counted := report.countedPorts(status, inUse); len(counted) > 0 {
		working := 0
		for _, ok := range counted {
			if ok {
				working++
			}
		}
		view["ports_total"] = len(counted)
		view["ports_working"] = working
		view["ports_failing"] = report.failingPorts(status, inUse)
		view["ports_age_seconds"] = time.Now().Unix() - report.PortsAt
	}
	return view
}
