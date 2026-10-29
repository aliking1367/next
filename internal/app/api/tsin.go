package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
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

const tsinServiceName = "T.Sin Countries"

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
}

func tokenSum(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) loadTSinSettings(ctx context.Context) (tsinSettings, error) {
	settings := tsinSettings{DC: "dc1", Target: xrayconfig.MasterTargetID, Transport: xrayconfig.TSinTransportTCP}
	var token, dc, countries, registeredDC, registeredSum, detail, target, transport sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT token, dc, countries, registered_dc, registered_token_hash,
last_applied_at, last_detail, target, transport FROM tsin_settings ORDER BY id LIMIT 1`).
		Scan(&token, &dc, &countries, &registeredDC, &registeredSum, &settings.LastAppliedAt, &detail, &target, &transport)
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
	return settings, nil
}

func (s *Server) saveTSinSettings(ctx context.Context, settings tsinSettings) error {
	countries, _ := json.Marshal(settings.Countries)
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE tsin_settings SET token = ?, dc = ?, countries = ?,
registered_dc = ?, registered_token_hash = ?, last_applied_at = ?, last_detail = ?, target = ?, transport = ?`,
			settings.Token, settings.DC, string(countries), settings.RegisteredDC, settings.RegisteredTokenSum,
			settings.LastAppliedAt, settings.LastDetail, settings.Target, settings.Transport)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected > 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO tsin_settings
(token, dc, countries, registered_dc, registered_token_hash, last_applied_at, last_detail, target, transport) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			settings.Token, settings.DC, string(countries), settings.RegisteredDC, settings.RegisteredTokenSum,
			settings.LastAppliedAt, settings.LastDetail, settings.Target, settings.Transport)
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
		"dc":           settings.DC,
		"datacenters":  tsin.Datacenters(),
		"preferred":    tsin.Preferred,
		"token_set":    strings.TrimSpace(settings.Token) != "",
		"countries":    settings.Countries,
		"applied":      len(settings.Countries) > 0 && settings.LastAppliedAt.Valid,
		"last_detail":  settings.LastDetail,
		"service_name": tsinServiceName,
		"target":       settings.Target,
		"transport":    settings.Transport,
		"node_script":  tsin.NodeScript(settings.DC),
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
	if strings.TrimSpace(settings.Token) != "" {
		status, err := tsin.NewClient().Status(ctx, settings.DC, settings.Token)
		if err != nil {
			view["account_error"] = err.Error()
		} else {
			view["account"] = status
		}
	}
	writeJSON(w, http.StatusOK, view)
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
	if err != nil {
		writeError(w, tsinProviderStatus, err.Error())
		return
	}
	proxies, missing := status.Resolve(names)
	if len(missing) > 0 {
		writeError(w, http.StatusUnprocessableEntity, "this account has no proxy for: "+strings.Join(missing, ", "))
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
		})
	}
	inbounds, err := s.configRepo.ApplyTSin(ctx, target, settings.Transport, countries, portBusy)
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
		"service_name":      tsinServiceName,
		"target":            target,
		"tunnel_up":         tunnelUp,
		"needs_node_script": !onPanelHost,
		"detail":            settings.LastDetail,
	}
	if onPanelHost && !tunnelUp {
		response["warning"] = "The tunnel has not completed a handshake. Check that UDP to the provider is not blocked from this server."
	}
	writeJSON(w, http.StatusOK, response)
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
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM services WHERE name = ?`, tsinServiceName).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	now := dbTimestamp(time.Now().UTC())
	res, err := tx.ExecContext(ctx,
		`INSERT INTO services (name, description, used_traffic, lifetime_used_traffic, users_usage, created_at, updated_at) VALUES (?, ?, 0, 0, 0, ?, ?)`,
		tsinServiceName, "Created by the T.Sin integration: one config per exit country.", now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
