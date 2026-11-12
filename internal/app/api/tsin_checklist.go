package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/tsin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The setup checklist walks an admin through T.Sin one step at a time. The
// panel checks each step itself, so a step turns green only when it really
// works, and a red one says why. The dashboard holds the wording; a step here
// is an id, a state, a reason code and the values the admin needs to copy.

type tsinStep struct {
	ID string `json:"id"`
	// State is "ok", "warn", "fail", "todo" (not done yet) or "skip" (cannot
	// be checked before an earlier step).
	State  string            `json:"state"`
	Reason string            `json:"reason,omitempty"`
	Params map[string]any    `json:"params,omitempty"`
	Values map[string]string `json:"values,omitempty"`
	Raw    string            `json:"raw,omitempty"`
}

// tsinChecklistClient asks T.Sin for the checklist; tests replace it.
var tsinChecklistClient = tsin.NewClient

func (s *Server) handleCoreTSinChecklist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	steps := s.tsinChecklist(ctx, settings, panelOrigin(r))
	done := 0
	for _, step := range steps {
		if step.State == "ok" {
			done++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps, "done": done, "total": len(steps)})
}

// panelOrigin is the address the admin reaches the panel at, as the
// dashboard sends it. Only an address on the host this request came to is
// taken, so the check below cannot be pointed at another server.
func panelOrigin(r *http.Request) string {
	parsed, err := url.Parse(strings.TrimSpace(r.URL.Query().Get("origin")))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	if !strings.EqualFold(parsed.Hostname(), hostWithoutPort(r.Host)) {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func hostWithoutPort(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return hostport
}

func (s *Server) tsinChecklist(ctx context.Context, settings tsinSettings, origin string) []tsinStep {
	onNode := settings.Target != xrayconfig.MasterTargetID
	applied := settings.LastAppliedAt.Valid && len(settings.Countries) > 0
	pathMode := settings.Transport == xrayconfig.TSinTransportPath
	var report *tsinNodeReport
	if onNode {
		report = s.tsinNode.load(settings.Target)
	}

	// 1 and 2: the token, and the datacenter answering for it. On a node that
	// holds the session, T.Sin answers only the node.
	token := tsinStep{ID: "token"}
	dc := tsinStep{ID: "datacenter", Params: map[string]any{"dc": settings.DC}}
	var status *tsin.Status
	switch {
	case !tsin.ValidToken(settings.Token):
		token.State, token.Reason = "todo", "no_token"
		dc.State = "skip"
	case report.fresh() && len(report.Account) > 0:
		if parsed, err := tsin.ParseStatus(report.Account); err == nil {
			status = &parsed
		}
		token.State, token.Reason = "ok", "via_node"
		dc.State, dc.Reason = "ok", "via_node"
		dc.Params["dc"] = firstNonEmpty(report.DC, settings.DC)
	default:
		started := time.Now()
		parsed, err := tsinChecklistClient().Status(ctx, settings.DC, settings.Token)
		code := tsin.Classify(err)
		switch {
		case err == nil:
			status = &parsed
			token.State = "ok"
			dc.State = "ok"
			dc.Params["latency_ms"] = time.Since(started).Milliseconds()
		case code == tsin.ProblemInUse && onNode:
			// Expected while the node holds the session and has not reported.
			token.State, token.Reason = "ok", "in_use_node"
			dc.State, dc.Reason = "ok", "in_use_node"
		case code == tsin.ProblemInUse:
			token.State, token.Reason, token.Raw = "warn", "in_use", err.Error()
			dc.State, dc.Reason = "ok", "in_use_node"
		case code == tsin.ProblemTokenRefused:
			token.State, token.Reason, token.Raw = "fail", "token_refused", err.Error()
			dc.State = "skip"
		default:
			token.State = "skip"
			dc.State, dc.Reason, dc.Raw = "fail", code, err.Error()
		}
	}
	if status != nil {
		token.Params = map[string]any{"client": status.ClientName, "countries": len(status.Proxies)}
		if status.ActiveKnown && !status.Active {
			token.State, token.Reason = "fail", "inactive"
		}
	}
	steps := []tsinStep{token, dc}

	// 3: the countries applied, and where to.
	apply := tsinStep{ID: "apply", Params: map[string]any{"count": len(settings.Countries), "target": s.tsinTargetName(ctx, settings.Target), "transport": settings.Transport}}
	if applied {
		apply.State = "ok"
	} else {
		apply.State, apply.Reason = "todo", "not_applied"
	}
	steps = append(steps, apply)

	// 4: in path mode, the Cloudflare side of the countries' domain.
	if pathMode {
		steps = append(steps, s.tsinGateStep(ctx, settings, applied))
		if len(settings.DirectCountries) > 0 {
			steps = append(steps, s.tsinDirectStep(ctx, settings, applied))
		}
	}

	// 5: the tunnel. On a node it is known only from what the node reports, so
	// this is also where running the node script is checked.
	if onNode {
		steps = append(steps, s.tsinNodeStep(ctx, settings, report, applied, origin))
	} else {
		tunnel := tsinStep{ID: "tunnel"}
		switch {
		case !applied:
			tunnel.State = "skip"
		case tsinTunnelHealthy(ctx, tsin.NewTunnel()):
			tunnel.State = "ok"
		default:
			tunnel.State, tunnel.Reason = "fail", "down"
		}
		steps = append(steps, tunnel)
	}

	// 6: every country answering, as the node last tested them.
	countries := tsinStep{ID: "countries"}
	switch {
	case !onNode:
		countries.State = "skip"
		if applied {
			countries.State, countries.Reason = "ok", "panel_watch"
		}
	case !report.fresh() || len(report.Ports) == 0:
		countries.State, countries.Reason = "skip", "no_test"
	default:
		failing := report.failingPorts(status, settings.Countries)
		counted := report.countedPorts(status, settings.Countries)
		countries.Params = map[string]any{"working": len(counted) - len(failing), "total": len(counted)}
		if len(failing) == 0 {
			countries.State = "ok"
		} else {
			countries.State, countries.Reason = "warn", "failing"
			countries.Params["failing"] = strings.Join(failing, ", ")
		}
	}
	steps = append(steps, countries)

	// 7: users given the service, which is how they get the configs.
	users := tsinStep{ID: "users"}
	if serviceID, err := findTSinServiceID(ctx, s.db); err == nil && serviceID != 0 {
		var count int64
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE service_id = ? AND COALESCE(status, '') <> 'deleted'`, serviceID).Scan(&count)
		users.Params = map[string]any{"service": s.tsinServiceName(ctx), "count": count}
		if count > 0 {
			users.State = "ok"
		} else {
			users.State, users.Reason = "todo", "no_users"
		}
	} else if applied {
		// Applied countries without their service: Apply makes it again.
		users.State, users.Reason = "todo", "no_service"
	} else {
		users.State = "skip"
	}
	return append(steps, users)
}

func (s *Server) tsinTargetName(ctx context.Context, target string) string {
	_, nodeID, err := xrayconfig.ParseTargetID(target)
	if err != nil || nodeID == nil {
		return ""
	}
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM nodes WHERE id = ?`, *nodeID).Scan(&name); err != nil {
		return target
	}
	return name
}

// tsinTargetAddress is the server the countries' domain must point at.
func (s *Server) tsinTargetAddress(ctx context.Context, target string) string {
	_, nodeID, err := xrayconfig.ParseTargetID(target)
	if err != nil || nodeID == nil {
		return ""
	}
	var address string
	_ = s.db.QueryRowContext(ctx, `SELECT address FROM nodes WHERE id = ?`, *nodeID).Scan(&address)
	return strings.TrimSpace(address)
}

// tsinGateStep checks the countries' domain end to end, the way a user's app
// reaches it: through Cloudflare on 443 to the gate, with a WebSocket
// handshake on one country's path.
func (s *Server) tsinGateStep(ctx context.Context, settings tsinSettings, applied bool) tsinStep {
	step := tsinStep{ID: "cloudflare", Values: map[string]string{}}
	host := strings.TrimSpace(settings.GateHost)
	step.Params = map[string]any{"host": host, "port": settings.GatePort}
	step.Values["record_name"] = host
	if address := s.tsinTargetAddress(ctx, settings.Target); address != "" {
		step.Values["record_value"] = address
	}
	step.Values["origin_port"] = fmt.Sprint(settings.GatePort)
	step.Values["origin_rule"] = fmt.Sprintf(`(http.host eq "%s")`, host)
	step.Values["skip_rule"] = fmt.Sprintf(` or (http.host eq "%s")`, host)
	if host == "" {
		step.State, step.Reason = "todo", "no_host"
		return step
	}
	if !applied {
		step.State = "skip"
		return step
	}
	path := s.tsinCountryPath(ctx)
	if path == "" {
		step.State, step.Reason = "fail", "no_path"
		return step
	}
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil {
		step.State, step.Reason, step.Raw = "fail", "dns", err.Error()
		return step
	}
	response, err := tsinProbe(ctx, http.MethodGet, "https://"+host+path, map[string]string{
		"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
		"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
	})
	if err != nil {
		step.State, step.Reason, step.Raw = "fail", "unreachable", err.Error()
		return step
	}
	step.Params["status"] = response.StatusCode
	step.State, step.Reason = tsinGateVerdict(response.StatusCode, response.Header)
	return step
}

// tsinGateVerdict reads the answer to a WebSocket handshake sent through
// Cloudflare to the gate: 101 is a working path; anything else names the
// setting that is wrong.
func tsinGateVerdict(code int, header http.Header) (state, reason string) {
	switch {
	case code == http.StatusSwitchingProtocols:
		return "ok", ""
	case header.Get("Cf-Ray") == "":
		return "fail", "not_proxied"
	case header.Get("Cf-Mitigated") != "":
		return "fail", "challenge"
	case code == http.StatusBadRequest:
		// Cloudflare spoke plain HTTP to the gate's TLS port: SSL "Flexible".
		return "fail", "ssl_flexible"
	case code == 525 || code == 526:
		// The gate's certificate is self-signed: "Full (strict)" refuses it.
		return "fail", "ssl_strict"
	case code >= 521 && code <= 523:
		return "fail", "origin_down"
	case code == http.StatusForbidden:
		return "fail", "waf_block"
	}
	return "fail", "gate_answer"
}

// tsinDirectStep checks that every direct config answers on its address and
// port. The panel connects from outside Iran, so this proves the address is
// set up on the server and the port open, not that Iran lets it through.
func (s *Server) tsinDirectStep(ctx context.Context, settings tsinSettings, applied bool) tsinStep {
	step := tsinStep{ID: "direct", Params: map[string]any{}, Values: map[string]string{}}
	address := settings.DirectAddress
	if address == "" {
		address = s.tsinTargetAddress(ctx, settings.Target)
	}
	step.Params["address"] = address
	if !applied {
		step.State = "skip"
		return step
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT inbound_tag FROM hosts WHERE inbound_tag LIKE 'tsin-%' AND inbound_tag LIKE ?`, "%"+xrayconfig.TSinDirectSuffix)
	if err != nil {
		step.State, step.Reason, step.Raw = "fail", "unreachable", err.Error()
		return step
	}
	var tags []string
	for rows.Next() {
		var tag string
		if rows.Scan(&tag) == nil {
			tags = append(tags, tag)
		}
	}
	_ = rows.Close()
	if len(tags) == 0 {
		step.State, step.Reason = "todo", "not_applied"
		return step
	}
	step.Params["count"] = len(tags)
	if address == "" {
		step.State, step.Reason = "ok", "untested"
		return step
	}
	var closed []string
	for _, tag := range tags {
		inbound, err := s.configRepo.GetInbound(ctx, tag)
		if err != nil {
			continue
		}
		port := fmt.Sprint(inbound["port"])
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(address, port))
		if err != nil {
			closed = append(closed, port)
			continue
		}
		_ = conn.Close()
	}
	if reach := s.iranReach.load(); reach != nil && reach.Address == address && reach.Error == "" {
		step.Params["iran_open"], step.Params["iran_total"] = reach.Open, reach.Total
		step.Params["iran_age_minutes"] = int64(time.Since(reach.CheckedAt).Minutes())
		if len(closed) == 0 && reach.blocked() {
			// It answers from outside but not from Iran: filtered there.
			step.State, step.Reason = "fail", "iran_blocked"
			return step
		}
	}
	if len(closed) == 0 {
		step.State = "ok"
		return step
	}
	step.State, step.Reason = "fail", "closed"
	step.Params["ports"] = strings.Join(closed, ", ")
	return step
}

// tsinCountryPath is the WebSocket path of one country inbound in path mode.
func (s *Server) tsinCountryPath(ctx context.Context) string {
	var tag string
	err := s.db.QueryRowContext(ctx, `SELECT inbound_tag FROM hosts WHERE inbound_tag LIKE 'tsin-%' AND inbound_tag <> ?
AND inbound_tag NOT LIKE ? AND inbound_tag NOT LIKE ? AND inbound_tag NOT LIKE ? AND inbound_tag NOT LIKE ? ORDER BY id LIMIT 1`, xrayconfig.TSinGateTag, "%"+xrayconfig.TSinDirectSuffix, "%"+xrayconfig.TSinFastlySuffix, xrayconfig.TSinLabPrefix+"%", "%"+xrayconfig.TSinTunnelSuffix).Scan(&tag)
	if err != nil {
		return ""
	}
	inbound, err := s.configRepo.GetInbound(ctx, tag)
	if err != nil {
		return ""
	}
	stream, _ := inbound["streamSettings"].(map[string]any)
	ws, _ := stream["wsSettings"].(map[string]any)
	path, _ := ws["path"].(string)
	return path
}

// tsinNodeStep checks that the node runs the script and reports, and when it
// does not, whether Cloudflare is what stops its reports.
func (s *Server) tsinNodeStep(ctx context.Context, settings tsinSettings, report *tsinNodeReport, applied bool, origin string) tsinStep {
	step := tsinStep{ID: "node", Params: map[string]any{"node": s.tsinTargetName(ctx, settings.Target)}, Values: map[string]string{}}
	step.Values["report_skip_rule"] = ` or (http.request.uri.path eq "/internal/tsin/report")`
	switch {
	case !applied:
		step.State = "skip"
		return step
	case report.fresh() && report.tunnelUp():
		step.State = "ok"
		step.Params["age"] = int64(time.Since(report.ReceivedAt).Seconds())
		return step
	case report.fresh():
		step.State, step.Reason = "fail", "tunnel_down"
		if n := len(report.Log); n > 0 {
			step.Raw = report.Log[n-1]
		}
		if code := tsin.ClassifyText(report.AccountError); code != "" {
			step.Params["problem"] = code
		}
		return step
	case report != nil:
		step.State, step.Reason = "fail", "silent"
		step.Params["minutes"] = int64(time.Since(report.ReceivedAt).Minutes())
		return step
	}
	// Never reported: the script was not run, or Cloudflare turns it away.
	step.State, step.Reason = "todo", "never"
	if origin == "" {
		return step
	}
	response, err := tsinProbe(ctx, http.MethodPost, origin+"/internal/tsin/report", map[string]string{
		"Authorization": "Bearer checklist", "Content-Type": "application/json",
	})
	if err == nil && response.Header.Get("Cf-Mitigated") != "" {
		step.State, step.Reason = "fail", "report_blocked"
	}
	return step
}

// tsinProbe makes one request and returns the response with its body read
// and closed. HTTP/1.1 is forced so a WebSocket upgrade can be answered.
func tsinProbe(ctx context.Context, method, target string, headers map[string]string) (*http.Response, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, method, target, nil)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	client := &http.Client{
		Transport: &http.Transport{
			Proxy:             http.ProxyFromEnvironment,
			TLSClientConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
			TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
			ForceAttemptHTTP2: false,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return nil, err
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		// An upgraded connection stays open; reading it would wait out the
		// timeout.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	}
	_ = response.Body.Close()
	return response, nil
}
