package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// handleCoreTSinLab shows the protocol lab (GET), makes it (POST action
// "create") or removes it (POST action "remove"); see xrayconfig/tsin_lab.go.
// Its links point at the front server when one is set, since that is the
// address users' direct configs use.
func (s *Server) handleCoreTSinLab(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	address := strings.TrimSpace(settings.DirectAddress)
	if address == "" {
		address = s.tsinTargetAddress(ctx, settings.Target)
	}
	if r.Method == http.MethodPost {
		var request struct {
			Action string `json:"action"`
		}
		if err := decodeOptionalJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !tsinApplyMu.TryLock() {
			writeError(w, http.StatusConflict, "another T.Sin change is still running")
			return
		}
		switch request.Action {
		case "create":
			_, err = s.configRepo.EnsureTSinLab(ctx, settings.Target, address, s.tsinPortBusy(settings.Target))
		case "remove":
			err = s.configRepo.RemoveTSinLab(ctx, settings.Target)
		default:
			tsinApplyMu.Unlock()
			writeError(w, http.StatusBadRequest, "send action: create or remove")
			return
		}
		tsinApplyMu.Unlock()
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		s.kickNodeOperationsSoon()
	}
	tests, err := s.configRepo.TSinLabTests(ctx, settings.Target, address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"address": address, "tests": tests})
}
