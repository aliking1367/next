package api

import (
	"context"
	"database/sql"
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
	if settings.EmergencyEnabled && settings.EmergencyAddress == "" {
		writeError(w, http.StatusUnprocessableEntity, "enter the Iranian tunnel's address first")
		return
	}

	ports, err := s.configRepo.EnsureTSinEmergency(ctx, settings.Target, s.tsinPortBusy(settings.Target))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to set up the emergency tunnel: "+err.Error())
		return
	}
	if err := s.configRepo.SetTSinEmergencyHosts(ctx, settings.EmergencyAddress, settings.EmergencyVMessPort, settings.EmergencySSPort, settings.EmergencyEnabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	serviceID, err := s.attachTSinService(ctx, []xrayconfig.TSinInbound{{Tag: xrayconfig.TSinEmergencyVMessTag}, {Tag: xrayconfig.TSinEmergencySSTag}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to add the emergency tunnel to the service: "+err.Error())
		return
	}
	// Who may use an inbound follows whether its host is shown, so the users
	// are resent either way; adding or dropping users needs no restart.
	if err := s.withTx(ctx, func(tx *sql.Tx) error {
		return enqueueAffectedServicesUsersTx(ctx, tx, map[int64]bool{serviceID: true})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.kickNodeOperationsSoon()
	if err := s.saveTSinSettings(ctx, settings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	view := s.tsinEmergencyView(ctx, settings)
	view["node_ports"] = ports
	writeJSON(w, http.StatusOK, view)
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
	return view
}
