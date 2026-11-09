package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aliking1367/next/internal/app/countryflag"
	"github.com/aliking1367/next/internal/app/logging"
	"github.com/aliking1367/next/internal/app/tsin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// T.Sin is an exit-IP service: the user connects to this panel as usual, and
// the panel sends the traffic of a country's config out through that country's
// proxy at the provider. See package tsin for how the pieces fit.
//
// Applying is limited to the master config and to the host this panel runs on,
// because the tunnel lives on one machine and so must the Xray that uses it.

// tsinServiceName is only the name a new service starts with. The admin may
// rename it to anything -- admins who create users should not see the
// provider's name -- so the service is found by the country hosts it holds,
// never by its name.
const tsinServiceName = "Countries"

// tsinLegacyServiceName is what earlier versions called it, still matched so
// an existing setup is picked up instead of getting a second service.
const tsinLegacyServiceName = "T.Sin Countries"

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// findTSinServiceID returns the service holding the country hosts, or the one
// with the old fixed name, or 0 when there is none yet.
func findTSinServiceID(ctx context.Context, q rowQuerier) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `SELECT sh.service_id FROM service_hosts sh
JOIN hosts h ON h.id = sh.host_id
WHERE h.inbound_tag LIKE 'tsin-%'
GROUP BY sh.service_id
ORDER BY COUNT(*) DESC, sh.service_id
LIMIT 1`).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	err = q.QueryRowContext(ctx, `SELECT id FROM services WHERE name = ?`, tsinLegacyServiceName).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Server) tsinServiceName(ctx context.Context) string {
	id, err := findTSinServiceID(ctx, s.db)
	if err != nil || id == 0 {
		return tsinServiceName
	}
	var name string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM services WHERE id = ?`, id).Scan(&name); err != nil {
		return tsinServiceName
	}
	return name
}

// tsinProviderStatus answers a failure that came from T.Sin rather than from
// this panel. It is deliberately not 502 or 504: behind Cloudflare those are
// replaced by Cloudflare's own error page, which hid the provider's actual
// reason ("token in use on another server") behind a generic message.
const tsinProviderStatus = http.StatusFailedDependency

var tsinApplyMu sync.Mutex

type tsinSettings struct {
	Token              string
	DC                 string
	Countries          []string
	RegisteredDC       string
	RegisteredTokenSum string
	LastAppliedAt      sql.NullTime
	LastDetail         string
	// Target is the Xray config the countries are served from: "master" (this
	// panel's own machine) or "node:<id>".
	Target string
	// Transport is "tcp" or "xhttp" for the country inbounds.
	Transport string
	// GateHost and GatePort are where path mode is reached: the Cloudflare
	// domain in every country config, and the origin port of its gate.
	GateHost string
	GatePort int
	// DirectCountries also get, in path mode, a config on the server's own IP
	// without Cloudflare. nil means never chosen.
	DirectCountries []string
	// DirectAddress is the extra IP those configs point at; empty uses the
	// server's own address.
	DirectAddress string
	// The emergency tunnel, for when T.Sin is down: an Iranian tunnel's
	// address and the ports it forwards on (0 for the same as the server's),
	// and whether its configs are shown.
	EmergencyAddress   string
	EmergencyVMessPort int
	EmergencySSPort    int
	EmergencyEnabled   bool
	// EmergencyProtocols is both, vmess or shadowsocks.
	EmergencyProtocols string
	// EmergencyAdmins turned the emergency tunnel on for their own users.
	EmergencyAdmins []int64
}

type tsinRequest struct {
	// Token replaces the stored one. Omitted keeps it.
	Token     *string  `json:"token"`
	DC        string   `json:"dc"`
	Countries []string `json:"countries"`
	// Target is "master" or "node:<id>". Empty keeps the one already chosen.
	Target string `json:"target"`
	// Transport is "tcp" or "xhttp". Empty keeps the one already chosen.
	Transport string `json:"transport"`
	// GateHost and GatePort configure path mode. Empty or zero keeps the stored
	// values.
	GateHost string `json:"gate_host"`
	GatePort int    `json:"gate_port"`
	// DirectCountries replaces the countries with a direct config. Omitted
	// keeps the stored list.
	DirectCountries *[]string `json:"direct_countries"`
	// DirectAddress replaces the address of the direct configs; "" clears it
	// and omitted keeps it.
	DirectAddress *string `json:"direct_address"`
}

func tokenSum(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) loadTSinSettings(ctx context.Context) (tsinSettings, error) {
	settings := tsinSettings{DC: "dc1", Target: xrayconfig.MasterTargetID, Transport: xrayconfig.TSinTransportTCP, GatePort: xrayconfig.DefaultTSinGatePort}
	var token, dc, countries, registeredDC, registeredSum, detail, target, transport, gateHost, direct, directAddress sql.NullString
	var gatePort sql.NullInt64
	var emergencyAddress, emergencyProtocols, emergencyAdmins sql.NullString
	var emergencyVMess, emergencySS sql.NullInt64
	var emergencyEnabled sql.NullBool
	err := s.db.QueryRowContext(ctx, `SELECT token, dc, countries, registered_dc, registered_token_hash,
last_applied_at, last_detail, target, transport, gate_host, gate_port, direct_countries, direct_address,
emergency_address, emergency_vmess_port, emergency_ss_port, emergency_enabled, emergency_protocols, emergency_admins FROM tsin_settings ORDER BY id LIMIT 1`).
		Scan(&token, &dc, &countries, &registeredDC, &registeredSum, &settings.LastAppliedAt, &detail, &target, &transport, &gateHost, &gatePort, &direct, &directAddress,
			&emergencyAddress, &emergencyVMess, &emergencySS, &emergencyEnabled, &emergencyProtocols, &emergencyAdmins)
	if errors.Is(err, sql.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	settings.Token = token.String
	if tsin.ValidDC(dc.String) {
		settings.DC = dc.String
	}
	_ = json.Unmarshal([]byte(countries.String), &settings.Countries)
	settings.RegisteredDC = registeredDC.String
	settings.RegisteredTokenSum = registeredSum.String
	settings.LastDetail = detail.String
	if strings.TrimSpace(target.String) != "" {
		settings.Target = target.String
	}
	settings.Transport = xrayconfig.NormalizeTSinTransport(transport.String)
	settings.GateHost = gateHost.String
	settings.GatePort = int(gatePort.Int64)
	settings.DirectAddress = directAddress.String
	settings.EmergencyAddress = emergencyAddress.String
	settings.EmergencyVMessPort = int(emergencyVMess.Int64)
	settings.EmergencySSPort = int(emergencySS.Int64)
	settings.EmergencyEnabled = emergencyEnabled.Bool
	settings.EmergencyProtocols = xrayconfig.NormalizeTSinEmergencyProtocols(emergencyProtocols.String)
	_ = json.Unmarshal([]byte(emergencyAdmins.String), &settings.EmergencyAdmins)
	if direct.Valid {
		settings.DirectCountries = []string{}
		_ = json.Unmarshal([]byte(direct.String), &settings.DirectCountries)
	}
	return settings, nil
}

func (s *Server) saveTSinSettings(ctx context.Context, settings tsinSettings) error {
	countries, _ := json.Marshal(settings.Countries)
	var emergencyAdmins any
	if len(settings.EmergencyAdmins) > 0 {
		encoded, _ := json.Marshal(settings.EmergencyAdmins)
		emergencyAdmins = string(encoded)
	}
	var direct any
	if settings.DirectCountries != nil {
		encoded, _ := json.Marshal(settings.DirectCountries)
		direct = string(encoded)
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE tsin_settings SET token = ?, dc = ?, countries = ?,
registered_dc = ?, registered_token_hash = ?, last_applied_at = ?, last_detail = ?, target = ?, transport = ?, gate_host = ?, gate_port = ?, direct_countries = ?, direct_address = ?,
emergency_address = ?, emergency_vmess_port = ?, emergency_ss_port = ?, emergency_enabled = ?, emergency_protocols = ?, emergency_admins = ?`,
			settings.Token, settings.DC, string(countries), settings.RegisteredDC, settings.RegisteredTokenSum,
			settings.LastAppliedAt, settings.LastDetail, settings.Target, settings.Transport, settings.GateHost, settings.GatePort, direct, settings.DirectAddress,
			settings.EmergencyAddress, settings.EmergencyVMessPort, settings.EmergencySSPort, settings.EmergencyEnabled, settings.EmergencyProtocols, emergencyAdmins)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected > 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tsin_settings
(token, dc, countries, registered_dc, registered_token_hash, last_applied_at, last_detail, target, transport, gate_host, gate_port, direct_countries, direct_address,
emergency_address, emergency_vmess_port, emergency_ss_port, emergency_enabled, emergency_protocols, emergency_admins) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			settings.Token, settings.DC, string(countries), settings.RegisteredDC, settings.RegisteredTokenSum,
			settings.LastAppliedAt, settings.LastDetail, settings.Target, settings.Transport, settings.GateHost, settings.GatePort, direct, settings.DirectAddress,
			settings.EmergencyAddress, settings.EmergencyVMessPort, settings.EmergencySSPort, settings.EmergencyEnabled, settings.EmergencyProtocols, emergencyAdmins)
		return err
	})
}

func (s *Server) handleCoreTSin(w http.ResponseWriter, r *http.Request) {
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.tsinView(w, r)
	case http.MethodDelete:
		s.tsinRemove(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) tsinView(w http.ResponseWriter, r *http.Request) {
	settings, err := s.loadTSinSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view := map[string]any{
		"dc":               settings.DC,
		"datacenters":      tsin.Datacenters(),
		"preferred":        tsin.Preferred,
		"token_set":        strings.TrimSpace(settings.Token) != "",
		"countries":        settings.Countries,
		"applied":          len(settings.Countries) > 0 && settings.LastAppliedAt.Valid,
		"last_detail":      settings.LastDetail,
		"service_name":     s.tsinServiceName(r.Context()),
		"target":           settings.Target,
		"transport":        settings.Transport,
		"gate_host":        settings.GateHost,
		"gate_port":        settings.GatePort,
		"direct_countries": settings.DirectCountries,
		"direct_address":   settings.DirectAddress,
		"emergency":        s.tsinEmergencyView(r.Context(), settings),
		"node_script":      tsin.NodeScriptReporting(settings.DC, s.tsinReportKey(r.Context())),
	}
	if targets, err := s.configRepo.ListConfigTargets(r.Context()); err == nil {
		view["targets"] = targets
	}
	if settings.LastAppliedAt.Valid {
		view["last_applied_at"] = settings.LastAppliedAt.Time.UTC().Format(time.RFC3339)
	}
	tunnel := tsin.NewTunnel()
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if when, ok := tunnel.Handshake(ctx); ok {
		view["tunnel"] = map[string]any{"up": time.Since(when) < 3*time.Minute, "handshake": when.UTC().Format(time.RFC3339)}
	} else {
		view["tunnel"] = map[string]any{"up": false}
	}
	if tsin.ValidDC(settings.RegisteredDC) {
		view["registered_dc"] = settings.RegisteredDC
	}
	s.tsinAccountView(ctx, settings, view)
	writeJSON(w, http.StatusOK, view)
}

// tsinAccountView adds the account and, for a node, what the node reported.
func (s *Server) tsinAccountView(ctx context.Context, settings tsinSettings, view map[string]any) {
	var report *tsinNodeReport
	if settings.Target != xrayconfig.MasterTargetID {
		report = s.tsinNode.load(settings.Target)
	}
	switch {
	case strings.TrimSpace(settings.Token) == "":
	case report.fresh():
		// The node holds the session, so it is the one T.Sin answers; asking
		// from here would only be refused as "in use".
		view["account_source"] = "node"
		var status *tsin.Status
		if len(report.Account) > 0 {
			if parsed, err := tsin.ParseStatus(report.Account); err == nil {
				status = &parsed
				view["account"] = parsed
			} else {
				view["account_error"] = err.Error()
			}
		} else {
			view["account_error"] = firstNonEmpty(report.AccountError, "the node got no account details from T.Sin")
		}
		view["node_report"] = tsinNodeView(report, status, settings.Countries)
		view["tunnel"] = map[string]any{"up": report.tunnelUp(), "handshake_age": report.HandshakeAge}
	default:
		started := time.Now()
		status, err := tsin.NewClient().Status(ctx, settings.DC, settings.Token)
		// The time of the status call is what the datacenter ranking shows too.
		view["latency_ms"] = time.Since(started).Milliseconds()
		if err != nil {
			view["account_error"] = err.Error()
		} else {
			view["account"] = status
		}
		if report != nil {
			view["node_report"] = tsinNodeView(report, nil, settings.Countries)
		}
	}
	if text, ok := view["account_error"].(string); ok {
		view["account_error_code"] = tsin.ClassifyText(text)
	}
}

func (s *Server) handleCoreTSinApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	var request tsinRequest
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !tsinApplyMu.TryLock() {
		writeError(w, http.StatusConflict, "another T.Sin change is still running")
		return
	}
	defer tsinApplyMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if request.Token != nil {
		settings.Token = strings.TrimSpace(*request.Token)
	}
	if strings.TrimSpace(request.DC) != "" {
		settings.DC = strings.TrimSpace(request.DC)
	}
	if !tsin.ValidDC(settings.DC) {
		writeError(w, http.StatusUnprocessableEntity, "unknown T.Sin datacenter")
		return
	}
	if !tsin.ValidToken(settings.Token) {
		writeError(w, http.StatusUnprocessableEntity, "enter the T.Sin token first")
		return
	}
	names := cleanTSinCountries(request.Countries)
	if len(names) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "choose at least one country")
		return
	}

	client := tsin.NewClient()
	status, err := client.Status(ctx, settings.DC, settings.Token)
	if tsin.Classify(err) == tsin.ProblemInUse {
		// The countries run on a node that holds the session, and T.Sin shows
		// the account only to that node now; its latest report has it.
		if fromNode, ok := s.tsinAccountFromNode(firstNonEmpty(request.Target, settings.Target)); ok {
			status, err = fromNode, nil
		}
	}
	if err != nil {
		writeError(w, tsinProviderStatus, err.Error())
		return
	}
	proxies, missing := status.Resolve(names)
	// A renewed or different subscription can offer fewer countries. Refusing
	// the whole apply over that left admins stuck, so the missing ones are
	// skipped and reported; their configs are retired with the rest.
	if len(proxies) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "this account offers none of the chosen countries")
		return
	}

	target, err := normalizeTSinTarget(firstNonEmpty(request.Target, settings.Target))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	onPanelHost := target == xrayconfig.MasterTargetID
	if strings.TrimSpace(request.Transport) != "" {
		settings.Transport = xrayconfig.NormalizeTSinTransport(request.Transport)
	}
	if strings.TrimSpace(request.GateHost) != "" {
		settings.GateHost = strings.TrimSpace(request.GateHost)
	}
	if request.GatePort > 0 {
		settings.GatePort = request.GatePort
	}
	if request.DirectCountries != nil {
		settings.DirectCountries = cleanTSinCountries(*request.DirectCountries)
	}
	if request.DirectAddress != nil {
		address, err := normalizeTSinDirectAddress(*request.DirectAddress)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		settings.DirectAddress = address
	}
	direct := map[string]bool{}
	for _, name := range settings.DirectCountries {
		direct[strings.ToLower(name)] = true
	}

	tunnel := tsin.NewTunnel()
	address := ""
	tunnelUp := false
	var portBusy func(int) bool
	if onPanelHost {
		// The tunnel is made here, on the machine the panel runs on.
		portBusy = localPortBusy
		sum := tokenSum(settings.Token)
		needRegister := settings.RegisteredDC != settings.DC || settings.RegisteredTokenSum != sum || !tunnel.ConfigExists()
		conf := ""
		if needRegister {
			if tsin.ValidDC(settings.RegisteredDC) && settings.RegisteredDC != settings.DC && settings.RegisteredTokenSum == sum {
				// Leaving a datacenter: drop the session there first, or the
				// new one may answer that the token is in use elsewhere, as
				// T.Sin's own client does when switching.
				_ = client.Disconnect(ctx, settings.RegisteredDC, settings.Token)
			}
			registration, err := client.Register(ctx, settings.DC, settings.Token)
			if err != nil {
				writeError(w, tsinProviderStatus, err.Error())
				return
			}
			if conf, err = tsin.BuildConfig(registration); err != nil {
				writeError(w, tsinProviderStatus, err.Error())
				return
			}
			if err := tunnel.Up(ctx, conf); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			settings.RegisteredDC, settings.RegisteredTokenSum = settings.DC, sum
		} else {
			conf, err = tunnel.ReadConfig()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, up := tunnel.Handshake(ctx); !up {
				if err := tunnel.Up(ctx, conf); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
		}
		tunnelUp = tunnel.WaitHandshake(ctx, 12*time.Second)
		address = tsin.ProxyAddress(conf)
	}
	// On a node the tunnel is made there by the script the dashboard shows; the
	// panel registers nothing for it, because the profile belongs to the node.

	countries := make([]xrayconfig.TSinCountry, 0, len(proxies))
	for _, proxy := range proxies {
		countries = append(countries, xrayconfig.TSinCountry{
			Port: proxy.Port, Name: proxy.Country, Code: tsinCountryCode(proxy.Country), Address: address,
			// Only path mode has a Cloudflare config to add a direct one beside.
			Direct: settings.Transport == xrayconfig.TSinTransportPath && direct[strings.ToLower(proxy.Country)],
		})
	}
	if !onPanelHost {
		// What the node listens on, as it last reported, so new configs only
		// take ports that are free there.
		portBusy = s.tsinPortBusy(target)
	}
	inbounds, err := s.configRepo.ApplyTSin(ctx, target, settings.Transport,
		xrayconfig.TSinGate{Host: settings.GateHost, Port: settings.GatePort, DirectAddress: settings.DirectAddress}, countries, portBusy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to configure the countries: "+err.Error())
		return
	}

	serviceID, err := s.attachTSinService(ctx, inbounds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to attach the countries to a service: "+err.Error())
		return
	}
	if !onPanelHost && tunnel.ConfigExists() {
		// The countries moved away: a tunnel left here would be an orphan that
		// still holds the account's one peer slot.
		if err := tunnel.Down(ctx); err != nil {
			logging.Warnf(logging.ComponentNode, "t.sin: could not remove the old local tunnel: %v", err)
		}
		settings.RegisteredDC, settings.RegisteredTokenSum = "", ""
	}

	settings.Target = target
	settings.Countries = make([]string, 0, len(proxies))
	for _, proxy := range proxies {
		settings.Countries = append(settings.Countries, proxy.Country)
	}
	settings.LastAppliedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	switch {
	case !onPanelHost:
		settings.LastDetail = "applied to " + target + "; the tunnel script must be run on that node"
	case !tunnelUp:
		settings.LastDetail = "applied, but the tunnel has not completed a handshake yet"
	default:
		settings.LastDetail = "applied"
	}
	if err := s.saveTSinSettings(ctx, settings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	logging.Infof(logging.ComponentNode, "t.sin: applied %d countries on %s to %s", len(countries), settings.DC, target)

	response := map[string]any{
		"inbounds":          inbounds,
		"service_id":        serviceID,
		"service_name":      s.tsinServiceName(ctx),
		"target":            target,
		"tunnel_up":         tunnelUp,
		"needs_node_script": !onPanelHost,
		"detail":            settings.LastDetail,
	}
	if onPanelHost && !tunnelUp {
		response["warning"] = "The tunnel has not completed a handshake. Check that UDP to the provider is not blocked from this server."
	}
	if len(missing) > 0 {
		response["skipped"] = missing
		note := "Skipped, not offered by this account: " + strings.Join(missing, ", ")
		if existing, ok := response["warning"].(string); ok {
			note = existing + " " + note
		}
		response["warning"] = note
	}
	writeJSON(w, http.StatusOK, response)
}

// normalizeTSinDirectAddress accepts an IP address or a host name, and ""
// for none.
func normalizeTSinDirectAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if ip := net.ParseIP(strings.Trim(value, "[]")); ip != nil {
		return ip.String(), nil
	}
	if len(value) <= 253 && tsinHostnamePattern.MatchString(value) {
		return strings.ToLower(value), nil
	}
	return "", errors.New("the direct address must be an IP address or a host name")
}

var tsinHostnamePattern = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,63}$`)

// tsinAccountFromNode is the account as the node running target last
// reported it, when that report is fresh.
func (s *Server) tsinAccountFromNode(target string) (tsin.Status, bool) {
	normalized, err := normalizeTSinTarget(target)
	if err != nil || normalized == xrayconfig.MasterTargetID {
		return tsin.Status{}, false
	}
	report := s.tsinNode.load(normalized)
	if !report.fresh() || len(report.Account) == 0 {
		return tsin.Status{}, false
	}
	status, err := tsin.ParseStatus(report.Account)
	return status, err == nil
}

// normalizeTSinTarget accepts "master" or "node:<id>" and nothing else.
func normalizeTSinTarget(value string) (string, error) {
	_, nodeID, err := xrayconfig.ParseTargetID(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("choose the panel server or one of the nodes")
	}
	if nodeID == nil {
		return xrayconfig.MasterTargetID, nil
	}
	return xrayconfig.NodeTargetID(*nodeID), nil
}

func (s *Server) tsinRemove(w http.ResponseWriter, r *http.Request) {
	if !tsinApplyMu.TryLock() {
		writeError(w, http.StatusConflict, "another T.Sin change is still running")
		return
	}
	defer tsinApplyMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := s.configRepo.RemoveTSinEmergency(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to remove the emergency tunnel: "+err.Error())
		return
	}
	if err := s.configRepo.RemoveTSin(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to remove the T.Sin configuration: "+err.Error())
		return
	}
	if err := tsin.NewTunnel().Down(ctx); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tsin_settings`); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"detail": "T.Sin removed"})
}

func cleanTSinCountries(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value == "" || len(value) > 60 || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
		if len(out) == 40 {
			break
		}
	}
	return out
}

// tsinCountryCode finds the flag for a provider country name. "France dc" is
// the provider's own datacenter in France, so it gets France's flag.
func tsinCountryCode(name string) string {
	if code := countryflag.Detect(strings.TrimSuffix(strings.TrimSpace(name), " dc")); code != "" {
		return code
	}
	return ""
}

func (s *Server) attachTSinService(ctx context.Context, inbounds []xrayconfig.TSinInbound) (int64, error) {
	tags := make([]string, 0, len(inbounds))
	for _, inbound := range inbounds {
		tags = append(tags, inbound.Tag)
	}
	var serviceID int64
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var findErr error
		if serviceID, findErr = findOrCreateTSinServiceTx(ctx, tx); findErr != nil {
			return findErr
		}
		flowChanged, _, findErr := ensureAutoProvisionServiceFlowTx(ctx, tx, serviceID)
		if findErr != nil {
			return findErr
		}
		beforeTags, findErr := serviceRuntimeInboundTagsTx(ctx, tx, serviceID)
		if findErr != nil {
			return findErr
		}
		hostIDs, findErr := hostIDsForInboundTagsTx(ctx, tx, tags)
		if findErr != nil {
			return findErr
		}
		assignments, findErr := mergedServiceHostAssignmentsTx(ctx, tx, serviceID, hostIDs)
		if findErr != nil {
			return findErr
		}
		if findErr := syncServiceHostsTx(ctx, tx, serviceID, assignments); findErr != nil {
			return findErr
		}
		afterTags, findErr := serviceRuntimeInboundTagsTx(ctx, tx, serviceID)
		if findErr != nil {
			return findErr
		}
		if flowChanged || !stringBoolMapsEqual(beforeTags, afterTags) {
			return enqueueAffectedServicesUsersTx(ctx, tx, map[int64]bool{serviceID: true})
		}
		return nil
	})
	return serviceID, err
}

func findOrCreateTSinServiceTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	id, err := findTSinServiceID(ctx, tx)
	if err != nil || id != 0 {
		return id, err
	}
	now := dbTimestamp(time.Now().UTC())
	// The description stays empty: admins see it, and it should not name the
	// provider either.
	res, err := tx.ExecContext(ctx,
		`INSERT INTO services (name, description, used_traffic, lifetime_used_traffic, users_usage, created_at, updated_at) VALUES (?, ?, 0, 0, 0, ?, ?)`,
		tsinServiceName, "", now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// handleCoreTSinDatacenters asks every T.Sin datacenter about the token at
// once and returns them fastest first, the ones that accept it on top. The
// dialog uses it to pick the best datacenter, as T.Sin's own client does.
func (s *Server) handleCoreTSinDatacenters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request struct {
		Token *string `json:"token"`
	}
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	token := ""
	if request.Token != nil && strings.TrimSpace(*request.Token) != "" {
		token = strings.TrimSpace(*request.Token)
	} else {
		settings, err := s.loadTSinSettings(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		token = settings.Token
	}
	if !tsin.ValidToken(token) {
		writeError(w, http.StatusUnprocessableEntity, "enter the T.Sin token first")
		return
	}
	ranked := tsin.NewClient().RankDatacenters(ctx, token)
	rows := make([]map[string]any, 0, len(ranked))
	best := ""
	for _, result := range ranked {
		row := map[string]any{"dc": result.DC, "ok": result.OK, "latency_ms": result.LatencyMS}
		if result.OK {
			row["countries"] = len(result.Status.Proxies)
			if best == "" {
				best = result.DC
			}
		} else {
			row["error"] = result.Error
			row["problem"] = tsin.ClassifyText(result.Error)
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"datacenters": rows, "best": best})
}
