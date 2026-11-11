package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The emergency tunnel is the admin's switch for the hours T.Sin is down:
// turned on, every user of the countries gets two more configs, VMess and
// Shadowsocks, that reach the countries' server through an Iranian tunnel and
// leave from that server directly. See xrayconfig.EnsureTSinEmergency.

type tsinEmergencyRequest struct {
	// Address is the Iranian tunnel's IP or host name. Omitted keeps it.
	Address *string `json:"address"`
	// VMessPort and SSPort are the ports the tunnel forwards on; 0 means the
	// same as the server's. Omitted keeps them.
	VMessPort *int `json:"vmess_port"`
	SSPort    *int `json:"ss_port"`
	// Enabled shows or hides the configs. Omitted keeps it.
	Enabled *bool `json:"enabled"`
	// Protocols is both, vmess or shadowsocks: which configs are shown, for
	// an admin with a single tunnel port. Omitted keeps it.
	Protocols *string `json:"protocols"`
	// Audience is "all", or "mci" to show the configs only to users who
	// update their subscription from Hamrah Aval. Omitted keeps it.
	Audience *string `json:"audience"`
}

func (s *Server) handleCoreTSinEmergency(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	var request tsinEmergencyRequest
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !tsinApplyMu.TryLock() {
		writeError(w, http.StatusConflict, "another T.Sin change is still running")
		return
	}
	defer tsinApplyMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !settings.LastAppliedAt.Valid || len(settings.Countries) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "apply the countries first; the emergency tunnel goes to the server they run on")
		return
	}
	if request.Address != nil {
		address, err := normalizeTSinDirectAddress(*request.Address)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "the tunnel address must be an IP address or a host name")
			return
		}
		settings.EmergencyAddress = address
	}
	for _, item := range []struct {
		value *int
		into  *int
	}{{request.VMessPort, &settings.EmergencyVMessPort}, {request.SSPort, &settings.EmergencySSPort}} {
		if item.value == nil {
			continue
		}
		if *item.value < 0 || *item.value > 65535 {
			writeError(w, http.StatusUnprocessableEntity, "a tunnel port must be between 1 and 65535, or 0 for the same as the server's")
			return
		}
		*item.into = *item.value
	}
	if request.Enabled != nil {
		settings.EmergencyEnabled = *request.Enabled
	}
	if request.Protocols != nil {
		settings.EmergencyProtocols = xrayconfig.NormalizeTSinEmergencyProtocols(*request.Protocols)
	}
	if request.Audience != nil {
		settings.EmergencyAudience = normalizeEmergencyAudience(*request.Audience)
	}
	if settings.EmergencyEnabled && settings.EmergencyAddress == "" {
		writeError(w, http.StatusUnprocessableEntity, "enter the Iranian tunnel's address first")
		return
	}

	ports, err := s.applyTSinEmergency(ctx, settings)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view := s.tsinEmergencyView(ctx, settings)
	view["node_ports"] = ports
	writeJSON(w, http.StatusOK, view)
}

// applyTSinEmergency puts the emergency tunnel in settings into effect and
// saves it: the inbounds on the countries' server, their hosts shown to
// everyone, to the chosen admins' users only, or to nobody, and the node
// told who may use them now.
func (s *Server) applyTSinEmergency(ctx context.Context, settings tsinSettings) (xrayconfig.TSinEmergencyPorts, error) {
	var ports xrayconfig.TSinEmergencyPorts
	// The emergency configs join the service the countries are in; without
	// country configs there is no such service to join yet.
	if serviceID, err := findTSinServiceID(ctx, s.db); err != nil {
		return ports, err
	} else if serviceID == 0 {
		return ports, errors.New("apply the countries first; the emergency tunnel joins their service")
	}
	ports, err := s.configRepo.EnsureTSinEmergency(ctx, settings.Target, s.tsinPortBusy(settings.Target))
	if err != nil {
		return ports, fmt.Errorf("Failed to set up the emergency tunnel: %w", err)
	}
	if err := s.configRepo.SetTSinEmergencyHosts(ctx, settings.EmergencyAddress, settings.EmergencyVMessPort, settings.EmergencySSPort,
		settings.EmergencyEnabled, settings.EmergencyProtocols, settings.EmergencyAdmins); err != nil {
		return ports, err
	}
	serviceID, err := s.attachTSinService(ctx, []xrayconfig.TSinInbound{{Tag: xrayconfig.TSinEmergencyVMessTag}, {Tag: xrayconfig.TSinEmergencySSTag}})
	if err != nil {
		return ports, fmt.Errorf("Failed to add the emergency tunnel to the service: %w", err)
	}
	// Who may use an inbound follows whether its host is shown, so the node
	// gets its users again either way.
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		return enqueueAffectedServicesUsersTx(ctx, tx, map[int64]bool{serviceID: true})
	}); err != nil {
		return ports, err
	}
	s.kickNodeOperationsSoon()
	return ports, s.saveTSinSettings(ctx, settings)
}

// tsinPortBusy says which ports are taken on target's machine, where that can
// be known: the panel's own, or a node that reports what listens on it.
func (s *Server) tsinPortBusy(target string) func(int) bool {
	if target == xrayconfig.MasterTargetID {
		return localPortBusy
	}
	report := s.tsinNode.load(target)
	if !report.fresh() || report.Listening == nil {
		return nil
	}
	listening := map[int]bool{}
	for _, port := range report.Listening {
		listening[port] = true
	}
	return func(port int) bool { return listening[port] }
}

// tsinEmergencyView is what the dialog shows of the emergency tunnel: its
// settings, and where the Iranian tunnel has to forward to.
func (s *Server) tsinEmergencyView(ctx context.Context, settings tsinSettings) map[string]any {
	view := map[string]any{
		"address":    settings.EmergencyAddress,
		"vmess_port": settings.EmergencyVMessPort,
		"ss_port":    settings.EmergencySSPort,
		"enabled":    settings.EmergencyEnabled,
		"protocols":  xrayconfig.NormalizeTSinEmergencyProtocols(settings.EmergencyProtocols),
	}
	ports := map[string]int{}
	for key, tag := range map[string]string{"vmess": xrayconfig.TSinEmergencyVMessTag, "shadowsocks": xrayconfig.TSinEmergencySSTag} {
		if inbound, err := s.configRepo.GetInbound(ctx, tag); err == nil {
			port, _ := inbound["port"].(float64)
			if port == 0 {
				if value, ok := inbound["port"].(int); ok {
					port = float64(value)
				}
			}
			ports[key] = int(port)
		}
	}
	if len(ports) > 0 {
		view["node_ports"] = ports
	}
	address := s.tsinTargetAddress(ctx, settings.Target)
	if address == "" && settings.Target == xrayconfig.MasterTargetID {
		address = "this server's IP"
	}
	view["node_address"] = strings.TrimSpace(address)
	view["audience"] = normalizeEmergencyAudience(settings.EmergencyAudience)
	view["operator_ranges"] = s.mobileRanges.summary()
	var mciUsers int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_subscription_access WHERE mci_seen_at > ?`,
		time.Now().UTC().Add(-emergencyMCIMemory)).Scan(&mciUsers)
	view["mci_users"] = mciUsers
	return view
}
