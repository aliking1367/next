package api

import (
	"context"
	"strings"
	"time"
)

// emergencyMCIMemory is how long a user who updated their subscription from
// Hamrah Aval keeps the emergency configs when a later update comes through
// a VPN (a foreign address), and how far back the dialog counts MCI users.
const emergencyMCIMemory = 7 * 24 * time.Hour

func normalizeEmergencyAudience(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), operatorMCI) {
		return operatorMCI
	}
	return "all"
}

// tsinEmergencyMCIOnly reads whether the emergency configs go to MCI users
// only. Any failure reads as no, so a database problem shows them as before.
func (s *Server) tsinEmergencyMCIOnly(ctx context.Context) bool {
	if s == nil || s.db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var audience *string
	if err := s.db.QueryRowContext(ctx, `SELECT emergency_audience FROM tsin_settings ORDER BY id LIMIT 1`).Scan(&audience); err != nil || audience == nil {
		return false
	}
	return normalizeEmergencyAudience(*audience) == operatorMCI
}
