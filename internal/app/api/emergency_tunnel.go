package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// handleEmergencyTunnel is the dashboard's switch for the T.Sin emergency
// tunnel. The owner's switch shows it to every user of the countries; an
// admin given the permission turns it on for their own users only. Setting it
// up (the Iranian tunnel's address and ports) stays in the Countries dialog.
func (s *Server) handleEmergencyTunnel(w http.ResponseWriter, r *http.Request) {
	principal, ok := r.Context().Value(adminContextKey).(adminPrincipal)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	owner := principal.Role == string(adminapp.RoleFullAccess)
	allowed := owner || principal.Context.Admin.Permissions.Users.EmergencyTunnel
	adminID := principal.Context.Admin.ID

	switch r.Method {
	case http.MethodGet:
		settings, err := s.loadTSinSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.emergencyTunnelState(r.Context(), settings, owner, allowed, adminID))
	case http.MethodPost:
		if !allowed {
			writeError(w, http.StatusForbidden, "You're not allowed to switch the emergency tunnel")
			return
		}
		var request struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeOptionalJSON(r, &request); err != nil || request.Enabled == nil {
			writeError(w, http.StatusBadRequest, "send enabled: true or false")
			return
		}
		if !tsinApplyMu.TryLock() {
			writeError(w, http.StatusConflict, "another change is still running; try again in a moment")
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
		if !s.emergencyTunnelConfigured(ctx, settings) {
			writeError(w, http.StatusUnprocessableEntity, "the emergency tunnel is not set up yet; the panel owner sets it up in the Countries window")
			return
		}
		if owner {
			settings.EmergencyEnabled = *request.Enabled
			if !*request.Enabled {
				// The owner turning it off means T.Sin is back: it goes off
				// for the admins who had turned it on for their users too.
				settings.EmergencyAdmins = nil
			}
		} else {
			admins := slices.DeleteFunc(slices.Clone(settings.EmergencyAdmins), func(id int64) bool { return id == adminID })
			if *request.Enabled {
				admins = append(admins, adminID)
			}
			settings.EmergencyAdmins = admins
		}
		if _, err := s.applyTSinEmergency(ctx, settings); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.emergencyTunnelState(ctx, settings, owner, allowed, adminID))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// emergencyTunnelConfigured reports whether the owner set the tunnel up: an
// address, and its inbounds made on the countries' server.
func (s *Server) emergencyTunnelConfigured(ctx context.Context, settings tsinSettings) bool {
	if settings.EmergencyAddress == "" || !settings.LastAppliedAt.Valid || len(settings.Countries) == 0 {
		return false
	}
	for _, tag := range xrayconfig.TSinEmergencyTags {
		if _, err := s.configRepo.GetInbound(ctx, tag); err == nil {
			return true
		}
	}
	return false
}

// emergencyTunnelState is what the dashboard shows: whether this admin may
// switch it, for whom, and whether it is on for them.
func (s *Server) emergencyTunnelState(ctx context.Context, settings tsinSettings, owner, allowed bool, adminID int64) map[string]any {
	scope := "mine"
	enabled := slices.Contains(settings.EmergencyAdmins, adminID)
	if owner {
		scope = "all"
		enabled = settings.EmergencyEnabled
	}
	state := map[string]any{
		"allowed":        allowed,
		"configured":     allowed && s.emergencyTunnelConfigured(ctx, settings),
		"scope":          scope,
		"enabled":        enabled,
		"global_enabled": settings.EmergencyEnabled,
	}
	if owner {
		state["admins_on"] = len(settings.EmergencyAdmins)
	}
	return state
}
