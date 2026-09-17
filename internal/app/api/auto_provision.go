package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"
)

// autoProvisionServiceName is the fixed name of the service that bundles the
// hosts created by the auto-provision button, so re-running it finds and
// reuses the same service instead of creating duplicates.
const autoProvisionServiceName = "Best Protocols (Auto)"

type autoProvisionResponse struct {
	ServiceID   int64  `json:"service_id"`
	ServiceName string `json:"service_name"`
	Protocols   []any  `json:"protocols"`
	Detail      string `json:"detail"`
}

// handleCoreAutoConfigure is the one-click "auto-configure best protocols"
// action: it creates a curated set of modern Xray inbounds (VLESS+REALITY,
// Trojan+REALITY, Hysteria2, Shadowsocks) on the master node and bundles
// their hosts into a single service, so a sudo admin only has to create a
// user afterward to hand out working access to all of them at once.
func (s *Server) handleCoreAutoConfigure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}

	result, err := s.configRepo.AutoProvisionBestProtocols(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to auto-configure protocols: "+err.Error())
		return
	}

	tags := make([]string, 0, len(result.Protocols))
	protocols := make([]any, 0, len(result.Protocols))
	for _, protocol := range result.Protocols {
		tags = append(tags, protocol.Tag)
		protocols = append(protocols, protocol)
	}

	var serviceID int64
	err = s.withTx(r.Context(), func(tx *sql.Tx) error {
		var findErr error
		serviceID, findErr = findOrCreateAutoProvisionServiceTx(r.Context(), tx)
		if findErr != nil {
			return findErr
		}
		hostIDs, findErr := hostIDsForInboundTagsTx(r.Context(), tx, tags)
		if findErr != nil {
			return findErr
		}
		assignments, findErr := mergedServiceHostAssignmentsTx(r.Context(), tx, serviceID, hostIDs)
		if findErr != nil {
			return findErr
		}
		return syncServiceHostsTx(r.Context(), tx, serviceID, assignments)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to attach auto-configured hosts to a service: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, autoProvisionResponse{
		ServiceID:   serviceID,
		ServiceName: autoProvisionServiceName,
		Protocols:   protocols,
		Detail:      "Best protocols configured",
	})
}

func findOrCreateAutoProvisionServiceTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM services WHERE name = ?`, autoProvisionServiceName).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	now := dbTimestamp(time.Now().UTC())
	res, err := tx.ExecContext(
		ctx,
		`INSERT INTO services (name, description, used_traffic, lifetime_used_traffic, users_usage, created_at, updated_at) VALUES (?, ?, 0, 0, 0, ?, ?)`,
		autoProvisionServiceName,
		"Automatically created by the auto-configure best protocols action.",
		now,
		now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func hostIDsForInboundTagsTx(ctx context.Context, tx *sql.Tx, tags []string) ([]int64, error) {
	ids := make([]int64, 0, len(tags))
	if len(tags) == 0 {
		return ids, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(tags)), ",")
	args := make([]any, len(tags))
	for i, tag := range tags {
		args[i] = tag
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM hosts WHERE inbound_tag IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// mergedServiceHostAssignmentsTx keeps any hosts already attached to the
// service (an admin may have added their own) and adds the newly
// auto-configured ones, so re-running the action never drops manual changes.
func mergedServiceHostAssignmentsTx(ctx context.Context, tx *sql.Tx, serviceID int64, newHostIDs []int64) ([]serviceHostAssignment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT host_id FROM service_hosts WHERE service_id = ? ORDER BY sort`, serviceID)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	assignments := make([]serviceHostAssignment, 0, len(newHostIDs))
	for rows.Next() {
		var hostID int64
		if err := rows.Scan(&hostID); err != nil {
			rows.Close()
			return nil, err
		}
		if !seen[hostID] {
			seen[hostID] = true
			assignments = append(assignments, serviceHostAssignment{HostID: hostID})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, hostID := range newHostIDs {
		if !seen[hostID] {
			seen[hostID] = true
			assignments = append(assignments, serviceHostAssignment{HostID: hostID})
		}
	}
	return assignments, nil
}
