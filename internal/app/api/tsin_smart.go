package api

import (
	"context"
	"net/http"
	"time"

	userapp "github.com/aliking1367/next/internal/app/user"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The smart config switch (user/smart_configs.go) lives in tsin_settings and
// only changes what subscriptions render: nothing is sent to the nodes and no
// Xray restarts when it is flipped.

// tsinSmartConfig reads the switch. Any failure reads as off, so a database
// problem can never change what a subscription looks like.
func (s *Server) tsinSmartConfig(ctx context.Context) int {
	if s == nil || s.db == nil {
		return userapp.SmartConfigOff
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var level int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(smart_config, 0) FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&level); err != nil {
		return userapp.SmartConfigOff
	}
	return userapp.NormalizeSmartConfig(level)
}

// handleCoreTSinSmart turns the smart config off (0), on in the JSON
// subscriptions apps already get (1), or on with V2Box also getting the JSON
// subscription (2).
func (s *Server) handleCoreTSinSmart(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"level": s.tsinSmartConfig(r.Context())})
		return
	case http.MethodPost:
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	var request struct {
		Level *int `json:"level"`
	}
	if err := decodeOptionalJSON(r, &request); err != nil || request.Level == nil {
		writeError(w, http.StatusBadRequest, "send level: 0, 1 or 2")
		return
	}
	level := *request.Level
	if level != userapp.SmartConfigOff && level != userapp.SmartConfigJSON && level != userapp.SmartConfigV2Box {
		writeError(w, http.StatusBadRequest, "send level: 0, 1 or 2")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if level != userapp.SmartConfigOff {
		settings, err := s.loadTSinSettings(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !settings.LastAppliedAt.Valid || settings.Transport != xrayconfig.TSinTransportPath || len(settings.DirectCountries) == 0 {
			writeError(w, http.StatusUnprocessableEntity, "the smart config needs the ⚡ direct configs: choose them and press Apply first")
			return
		}
	}
	result, err := s.db.ExecContext(ctx, `UPDATE tsin_settings SET smart_config = ?`, level)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if rows, err := result.RowsAffected(); err == nil && rows == 0 {
		writeError(w, http.StatusUnprocessableEntity, "apply the countries first")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": s.tsinSmartConfig(r.Context())})
}
