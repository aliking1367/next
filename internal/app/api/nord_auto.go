package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// NordVPN auto configs: when on, every NordVPN location added as an outbound
// on the target gets its own config on the chosen service (see
// xrayconfig.SyncNordAuto). Separate from T.Sin: its own settings row, its own
// endpoint, its own inbounds and rules. It runs after the core settings page
// saves the target's config, and on demand.

type nordAutoSettings struct {
	Enabled    bool              `json:"enabled"`
	Target     string            `json:"target"`
	Address    string            `json:"address"`
	SNI        string            `json:"sni"`
	ServiceID  int64             `json:"service_id"`
	AIOutbound string            `json:"ai_outbound"`
	FrontPort  int               `json:"front_port"`
	Managed    map[string]string `json:"-"`
}

var (
	nordAutoMu        sync.Mutex
	nordAutoLastError string
	nordAutoLastRun   time.Time
)

// loadNordAutoSettings reads the row, filling unset fields from T.Sin's own
// settings (its target, entry address and service) as sensible defaults.
func (s *Server) loadNordAutoSettings(ctx context.Context) (nordAutoSettings, error) {
	settings := nordAutoSettings{Managed: map[string]string{}}
	var enabled sql.NullBool
	var target, address, sni, aiOutbound, managed sql.NullString
	var serviceID, frontPort sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT enabled, target, address, sni, service_id, ai_outbound, managed, front_port FROM nord_auto_settings WHERE id = 1`).
		Scan(&enabled, &target, &address, &sni, &serviceID, &aiOutbound, &managed, &frontPort)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return settings, err
	}
	settings.Enabled = enabled.Bool
	settings.Target = strings.TrimSpace(target.String)
	settings.Address = strings.TrimSpace(address.String)
	settings.SNI = strings.TrimSpace(sni.String)
	settings.ServiceID = serviceID.Int64
	settings.AIOutbound = strings.TrimSpace(aiOutbound.String)
	settings.FrontPort = int(frontPort.Int64)
	if managed.String != "" {
		_ = json.Unmarshal([]byte(managed.String), &settings.Managed)
	}
	if settings.Target == "" || settings.Address == "" || settings.ServiceID == 0 {
		if tsinSettings, err := s.loadTSinSettings(ctx); err == nil {
			if settings.Target == "" {
				settings.Target = tsinSettings.Target
			}
			if settings.Address == "" {
				settings.Address = strings.TrimSpace(tsinSettings.DirectAddress)
			}
		}
		if settings.ServiceID == 0 {
			if id, err := findTSinServiceID(ctx, s.db); err == nil {
				settings.ServiceID = id
			}
		}
	}
	if settings.Target == "" {
		settings.Target = xrayconfig.MasterTargetID
	}
	if settings.SNI == "" {
		settings.SNI = xrayconfig.DefaultTSinVisionSNI
	}
	return settings, nil
}

func (s *Server) saveNordAutoSettings(ctx context.Context, settings nordAutoSettings) error {
	managed, _ := json.Marshal(settings.Managed)
	var serviceID any
	if settings.ServiceID > 0 {
		serviceID = settings.ServiceID
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE nord_auto_settings SET enabled = ?, target = ?, address = ?, sni = ?, service_id = ?, ai_outbound = ?, managed = ?, front_port = ? WHERE id = 1`,
			settings.Enabled, settings.Target, settings.Address, settings.SNI, serviceID, settings.AIOutbound, string(managed), settings.FrontPort)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err == nil && affected > 0 {
			return nil
		}
		// MySQL counts an UPDATE that changes nothing as zero rows, so a row
		// that is already there and unchanged must not be inserted again.
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nord_auto_settings WHERE id = 1`).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO nord_auto_settings (id, enabled, target, address, sni, service_id, ai_outbound, managed, front_port) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)`,
			settings.Enabled, settings.Target, settings.Address, settings.SNI, serviceID, settings.AIOutbound, string(managed), settings.FrontPort)
		return err
	})
}

// runNordAuto syncs the configs when the automation is on (and, with target
// given, only when it is the automation's target). It never fails the caller's
// own work: the error is kept for the dialog to show.
func (s *Server) runNordAuto(ctx context.Context, target string) (xrayconfig.NordAutoResult, error) {
	nordAutoMu.Lock()
	defer nordAutoMu.Unlock()
	settings, err := s.loadNordAutoSettings(ctx)
	if err != nil || !settings.Enabled {
		return xrayconfig.NordAutoResult{}, err
	}
	if target != "" && target != settings.Target {
		return xrayconfig.NordAutoResult{}, nil
	}
	result, err := s.configRepo.SyncNordAuto(ctx, xrayconfig.NordAutoOptions{
		Target: settings.Target, Address: settings.Address, SNI: settings.SNI,
		AIOutbound: settings.AIOutbound, Managed: settings.Managed, PortBusy: s.tsinPortBusy(settings.Target),
		FrontPort: settings.FrontPort,
	})
	nordAutoLastRun = time.Now().UTC()
	if result.Managed != nil {
		settings.Managed = result.Managed
		// The AI location was removed: AI sites go out through each location
		// again, and the dialog shows that instead of a location that is gone.
		if settings.AIOutbound != "" && err == nil {
			found := false
			for _, location := range result.Locations {
				if location.Outbound == settings.AIOutbound {
					found = true
				}
			}
			if !found {
				settings.AIOutbound = ""
			}
		}
		if saveErr := s.saveNordAutoSettings(ctx, settings); saveErr != nil && err == nil {
			err = saveErr
		}
	}
	if err == nil && len(result.Created) > 0 && settings.ServiceID > 0 {
		err = s.attachNordAutoHosts(ctx, settings.ServiceID, result.Created)
	}
	if err != nil {
		nordAutoLastError = err.Error()
	} else {
		nordAutoLastError = ""
	}
	return result, err
}

// attachNordAutoHosts adds the new configs' hosts to the service, keeping the
// hosts it already has, and syncs its users onto the new inbounds.
func (s *Server) attachNordAutoHosts(ctx context.Context, serviceID int64, tags []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var exists int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM services WHERE id = ?`, serviceID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("the service chosen for the NordVPN configs no longer exists")
			}
			return err
		}
		beforeTags, err := serviceRuntimeInboundTagsTx(ctx, tx, serviceID)
		if err != nil {
			return err
		}
		hostIDs, err := hostIDsForInboundTagsTx(ctx, tx, tags)
		if err != nil {
			return err
		}
		assignments, err := mergedServiceHostAssignmentsTx(ctx, tx, serviceID, hostIDs)
		if err != nil {
			return err
		}
		if err := syncServiceHostsTx(ctx, tx, serviceID, assignments); err != nil {
			return err
		}
		afterTags, err := serviceRuntimeInboundTagsTx(ctx, tx, serviceID)
		if err != nil {
			return err
		}
		if !stringBoolMapsEqual(beforeTags, afterTags) {
			return enqueueAffectedServicesUsersTx(ctx, tx, map[int64]bool{serviceID: true})
		}
		return nil
	})
}

// nordAutoView is what the dialog shows: the settings, the target's NordVPN
// locations and the config made for each.
func (s *Server) nordAutoView(ctx context.Context) (map[string]any, error) {
	settings, err := s.loadNordAutoSettings(ctx)
	if err != nil {
		return nil, err
	}
	locations := []map[string]any{}
	if config, err := s.configRepo.GetTargetRawConfig(ctx, settings.Target); err == nil {
		ports := map[string]any{}
		for _, item := range asSlice(config["inbounds"]) {
			inbound, _ := item.(map[string]any)
			if tag, _ := inbound["tag"].(string); tag != "" {
				ports[tag] = inbound["port"]
			}
		}
		for _, item := range asSlice(config["outbounds"]) {
			outbound, _ := item.(map[string]any)
			tag, _ := outbound["tag"].(string)
			protocol, _ := outbound["protocol"].(string)
			if !strings.HasPrefix(tag, xrayconfig.NordOutboundPrefix) || !strings.EqualFold(protocol, "wireguard") {
				continue
			}
			inbound := settings.Managed[tag]
			entry := map[string]any{
				"outbound": tag,
				"inbound":  inbound,
				"remark":   xrayconfig.NordLocationRemark(xrayconfig.NordLocationCode(tag)),
			}
			if inbound != "" {
				entry["port"] = ports[inbound]
				if settings.FrontPort > 0 {
					entry["front_port"] = settings.FrontPort
				}
			}
			locations = append(locations, entry)
		}
	}
	view := map[string]any{
		"enabled":     settings.Enabled,
		"target":      settings.Target,
		"address":     settings.Address,
		"sni":         settings.SNI,
		"service_id":  settings.ServiceID,
		"ai_outbound": settings.AIOutbound,
		"front_port":  settings.FrontPort,
		"ai_domains":  xrayconfig.NordAutoAIDomains,
		"locations":   locations,
		"last_error":  nordAutoLastError,
	}
	if !nordAutoLastRun.IsZero() {
		view["last_run"] = nordAutoLastRun.Format(time.RFC3339)
	}
	return view, nil
}

func asSlice(value any) []any {
	items, _ := value.([]any)
	return items
}

func (s *Server) handleCoreNordAuto(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var payload struct {
			Enabled    *bool   `json:"enabled"`
			Target     *string `json:"target"`
			Address    *string `json:"address"`
			SNI        *string `json:"sni"`
			ServiceID  *int64  `json:"service_id"`
			AIOutbound *string `json:"ai_outbound"`
			FrontPort  *int    `json:"front_port"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		nordAutoMu.Lock()
		settings, err := s.loadNordAutoSettings(r.Context())
		if err == nil {
			if payload.Enabled != nil {
				settings.Enabled = *payload.Enabled
			}
			if payload.Target != nil && strings.TrimSpace(*payload.Target) != "" {
				settings.Target = strings.TrimSpace(*payload.Target)
			}
			if payload.Address != nil {
				settings.Address = strings.TrimSpace(*payload.Address)
			}
			if payload.SNI != nil {
				if sni, sniErr := xrayconfig.NormalizeTSinVisionSNI(*payload.SNI); sniErr != nil {
					err = sniErr
				} else {
					settings.SNI = sni
				}
			}
			if payload.ServiceID != nil {
				settings.ServiceID = *payload.ServiceID
			}
			if payload.AIOutbound != nil {
				settings.AIOutbound = strings.TrimSpace(*payload.AIOutbound)
			}
			if payload.FrontPort != nil {
				if *payload.FrontPort < 0 || *payload.FrontPort > 65535 {
					err = errors.New("the shared port must be between 1 and 65535, or empty for none")
				} else {
					settings.FrontPort = *payload.FrontPort
				}
			}
			if err == nil && settings.Enabled && settings.Address == "" {
				err = errors.New("set the address the configs connect to (the entry server)")
			}
			if err == nil {
				err = s.saveNordAutoSettings(r.Context(), settings)
			}
		}
		nordAutoMu.Unlock()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := s.runNordAuto(r.Context(), ""); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.kickNodeOperationsSoon()
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	view, err := s.nordAutoView(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleCoreNordAutoSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := s.runNordAuto(r.Context(), ""); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.kickNodeOperationsSoon()
	view, err := s.nordAutoView(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}
