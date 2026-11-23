package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// tsinAddressesRequest changes only where the countries' configs point. It is
// the part of the T.Sin settings that needs no Apply: nothing is sent to Xray,
// so nobody is disconnected and the node does not reload.
type tsinAddressesRequest struct {
	// DirectAddress replaces the address of the ⚡ and 💎 configs; several,
	// separated by commas, are picked at random per subscription. "" is the
	// server's own IP; omitted keeps it.
	DirectAddress *string `json:"direct_address"`
	// TunnelPorts replaces, per port on the server, the address (a comma list
	// is fine) and tunnel-side port users reach it on. Only rows that differ
	// from the saved ones move their countries' configs. Omitted keeps them.
	TunnelPorts *[]tsinTunnelPort `json:"tunnel_ports"`
}

// handleCoreTSinAddresses saves new addresses for the countries' configs and
// rewrites only the host rows. Users get them on their next subscription
// update.
func (s *Server) handleCoreTSinAddresses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	var request tsinAddressesRequest
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !tsinApplyMu.TryLock() {
		writeError(w, http.StatusConflict, "another T.Sin change is still running")
		return
	}
	defer tsinApplyMu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if request.DirectAddress != nil {
		address, err := normalizeTSinDirectAddress(*request.DirectAddress)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		settings.DirectAddress = address
	}

	saved := s.loadTSinTunnelPorts(ctx)
	ports := saved
	if request.TunnelPorts != nil {
		gatePort := settings.GatePort
		if gatePort <= 0 {
			gatePort = xrayconfig.DefaultTSinGatePort
		}
		cleaned, err := cleanTSinTunnelPorts(*request.TunnelPorts, gatePort)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		ports = cleaned
	}
	tunnels := tsinChangedTunnelTargets(saved, ports, s.loadTSinTunnelCountries(ctx), settings.Countries, settings.EmergencyAddress)

	var direct *string
	if request.DirectAddress != nil {
		direct = &settings.DirectAddress
	}
	changed, err := s.configRepo.SetTSinAddresses(ctx, direct, tunnels)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update the configs: "+err.Error())
		return
	}
	if request.DirectAddress != nil {
		if err := s.saveTSinSettings(ctx, settings); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if request.TunnelPorts != nil {
		if err := s.saveTSinTunnelPorts(ctx, ports); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	logging.Infof(logging.ComponentNode, "t.sin: addresses saved without apply, %d configs changed", changed)
	writeJSON(w, http.StatusOK, map[string]any{"changed": changed, "direct_address": settings.DirectAddress})
}

// tsinChangedTunnelTargets is, for every applied country behind a tunnel port
// whose row changed, where its tunnel config should now point. Countries
// behind unchanged rows are left alone, so configs an admin moved by hand
// keep their address.
func tsinChangedTunnelTargets(saved, ports []tsinTunnelPort, countries map[string]int, applied []string, defaultHost string) map[string]xrayconfig.TSinTunnelTarget {
	before := map[int]tsinTunnelPort{}
	for _, item := range saved {
		before[item.NodePort] = item
	}
	after := map[int]tsinTunnelPort{}
	for _, item := range ports {
		after[item.NodePort] = item
	}
	out := map[string]xrayconfig.TSinTunnelTarget{}
	for _, name := range applied {
		nodePort := countries[strings.ToLower(name)]
		if nodePort <= 0 {
			continue
		}
		row, ok := after[nodePort]
		if old, had := before[nodePort]; ok && had && old == row {
			continue
		}
		if !ok && !hadPort(before, nodePort) {
			continue
		}
		address := strings.TrimSpace(row.Address)
		if address == "" {
			address = strings.TrimSpace(defaultHost)
		}
		port := row.Port
		if port <= 0 {
			port = nodePort
		}
		out[name] = xrayconfig.TSinTunnelTarget{Address: address, Port: port}
	}
	return out
}

func hadPort(rows map[int]tsinTunnelPort, port int) bool {
	_, ok := rows[port]
	return ok
}
