package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"

	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The emergency backups are configs of the countries' service that the panel
// did not make for T.Sin (a config on the server's own exit through a front,
// say) and that are wanted only while T.Sin is down. The admin ticks them in
// the emergency section; the emergency switch then shows and hides them with
// the emergency configs. They are not tied to the Hamrah Aval audience, which
// is about the Iranian tunnel, and the countries' tunnel configs are not
// touched by the switch.

type tsinBackupCandidate struct {
	ID     int64  `json:"id"`
	Remark string `json:"remark"`
	Tag    string `json:"tag"`
}

// tsinBackupCandidates lists the configs of the countries' service that can be
// a backup: every host there except the ones T.Sin and the emergency tunnel
// make themselves.
func (s *Server) tsinBackupCandidates(ctx context.Context) []tsinBackupCandidate {
	serviceID, err := findTSinServiceID(ctx, s.db)
	if err != nil || serviceID == 0 {
		return []tsinBackupCandidate{}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT h.id, COALESCE(h.remark, ''), h.inbound_tag FROM hosts h
JOIN service_hosts sh ON sh.host_id = h.id WHERE sh.service_id = ? ORDER BY sh.sort, h.id`, serviceID)
	if err != nil {
		return []tsinBackupCandidate{}
	}
	defer rows.Close()
	out := []tsinBackupCandidate{}
	for rows.Next() {
		var item tsinBackupCandidate
		if err := rows.Scan(&item.ID, &item.Remark, &item.Tag); err != nil {
			return out
		}
		if strings.HasPrefix(item.Tag, xrayconfig.TSinInboundPrefix) ||
			item.Tag == xrayconfig.TSinEmergencyVMessTag || item.Tag == xrayconfig.TSinEmergencySSTag {
			continue
		}
		out = append(out, item)
	}
	return out
}

// loadTSinBackupHosts reads the chosen backups; any failure reads as none.
func (s *Server) loadTSinBackupHosts(ctx context.Context) []int64 {
	var raw sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT emergency_backup_hosts FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&raw); err != nil {
		return []int64{}
	}
	out := []int64{}
	if strings.TrimSpace(raw.String) != "" {
		_ = json.Unmarshal([]byte(raw.String), &out)
	}
	return out
}

func (s *Server) saveTSinBackupHosts(ctx context.Context, ids []int64) error {
	if ids == nil {
		ids = []int64{}
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE tsin_settings SET emergency_backup_hosts = ?`, string(encoded))
	return err
}

// cleanTSinBackupHosts keeps only ids that are backup candidates now, once
// each and in order.
func cleanTSinBackupHosts(ids []int64, candidates []tsinBackupCandidate) []int64 {
	allowed := map[int64]bool{}
	for _, item := range candidates {
		allowed[item.ID] = true
	}
	seen := map[int64]bool{}
	out := []int64{}
	for _, id := range ids {
		if allowed[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// setTSinBackupHosts shows the backups while the emergency tunnel is on and
// hides them while it is off, the way SetTSinEmergencyHosts treats the
// emergency configs, including the admins who turned it on for their own
// users. Users are queued for an update by the caller.
func (s *Server) setTSinBackupHosts(ctx context.Context, ids []int64, enabled bool, enabledAdmins []int64) error {
	disabled := 1
	if enabled {
		disabled = 0
	}
	var admins any
	if len(enabledAdmins) > 0 {
		encoded, _ := json.Marshal(enabledAdmins)
		admins = string(encoded)
	}
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, `UPDATE hosts SET is_disabled = ?, enabled_admins = ? WHERE id = ?`, disabled, admins, id); err != nil {
			return err
		}
	}
	return nil
}
