package nodecontroller

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/aliking1367/next/internal/app/logging"
)

// A host can be hidden for everyone and still shown to the users of chosen
// admins: hosts.enabled_admins lists them. The T.Sin emergency tunnel uses it
// so an admin can turn it on for their own users only. Who may use an inbound
// follows the same rule as who sees its config, so a config left in an app
// after it was hidden reaches nothing.

// adminAllowedTags is, per service and admin, the inbound tags of hidden hosts
// shown to that admin's users.
type adminAllowedTags map[int64]map[int64]map[string]bool

func (a adminAllowedTags) allows(serviceID, adminID sql.NullInt64, tag string) bool {
	if a == nil || !serviceID.Valid || !adminID.Valid {
		return false
	}
	return a[serviceID.Int64][adminID.Int64][tag]
}

// AdminAllowedTags reads which hidden hosts are shown to which admins' users.
// It never fails the caller: on any error it logs and returns none, so a
// problem here cannot stop every config from being built.
func (r Repository) AdminAllowedTags(ctx context.Context) adminAllowedTags {
	rows, err := r.db.QueryContext(ctx, `
SELECT sh.service_id, h.inbound_tag, COALESCE(h.enabled_admins, '')
FROM service_hosts sh
JOIN hosts h ON h.id = sh.host_id
WHERE COALESCE(h.is_disabled, 0) <> 0 AND COALESCE(h.enabled_admins, '') <> ''`)
	if err != nil {
		logging.Warnf(logging.ComponentNode, "per-admin hosts unreadable, none applied: %v", err)
		return nil
	}
	defer rows.Close()
	result := adminAllowedTags{}
	for rows.Next() {
		var serviceID int64
		var tag, raw string
		if err := rows.Scan(&serviceID, &tag, &raw); err != nil {
			logging.Warnf(logging.ComponentNode, "per-admin hosts unreadable, none applied: %v", err)
			return nil
		}
		var admins []int64
		if json.Unmarshal([]byte(strings.TrimSpace(raw)), &admins) != nil {
			continue
		}
		for _, adminID := range admins {
			if result[serviceID] == nil {
				result[serviceID] = map[int64]map[string]bool{}
			}
			if result[serviceID][adminID] == nil {
				result[serviceID][adminID] = map[string]bool{}
			}
			result[serviceID][adminID][tag] = true
		}
	}
	return result
}
