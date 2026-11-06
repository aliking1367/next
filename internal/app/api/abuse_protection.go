package api

import (
	"encoding/json"
	"net/http"

	"github.com/aliking1367/next/internal/app/logging"
)

// handleAbuseProtection reads or flips the switch that blocks BitTorrent and
// outgoing mail on port 25 on every node (see nodecontroller/abuse.go).
// Flipping it sends every node its config again, so it applies at once.
func (s *Server) handleAbuseProtection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.settingsRepo.RuntimeSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": settings.AbuseProtection})
	case http.MethodPut:
		principal, _ := r.Context().Value(adminContextKey).(adminPrincipal)
		if principal.Role != "full_access" && (principal.Role != "sudo" || !principal.Context.Admin.Permissions.Sudo.Settings) {
			writeError(w, http.StatusForbidden, "You're not allowed")
			return
		}
		var request struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decodeOptionalJSON(r, &request); err != nil || request.Enabled == nil {
			writeError(w, http.StatusBadRequest, "send enabled: true or false")
			return
		}
		value, _ := json.Marshal(*request.Enabled)
		settings, err := s.settingsRepo.UpdateRuntimeSettings(r.Context(), map[string]json.RawMessage{"abuse_protection": value})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.nodeControllerQueueSync(r.Context(), nil, map[string]any{"source": "abuse_protection"}); err != nil {
			logging.Warnf(logging.ComponentNode, "abuse protection: could not queue the node configs: %v", err)
		} else {
			s.kickNodeOperationsSoon()
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": settings.AbuseProtection})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
