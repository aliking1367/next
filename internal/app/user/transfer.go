package user

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
)

// transferChunk keeps each IN list well under SQLite's parameter limit.
const transferChunk = 400

// TransferUsersToAdminTx hands the given users to destination. Only the
// owner changes: links, configs and limits stay as they are. Traffic already
// used stays on the previous admin's books, because admin usage is counted as
// it happens, so only what the users use from now on reaches destination.
//
// Users the previous admin's own limit had switched off come back on, since
// that limit no longer applies to them. The ids of those users are returned
// so the caller can enable them on the nodes.
func TransferUsersToAdminTx(ctx context.Context, tx *sql.Tx, userIDs []int64, destination adminapp.Admin, now time.Time) ([]int64, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	if destination.Status != adminapp.StatusActive {
		return nil, clientError(400, fmt.Sprintf("%s is not active, so it cannot take users.", destination.Username))
	}

	type transferRow struct {
		id        int64
		serviceID sql.NullInt64
		status    string
		suspended bool
	}
	rows := make([]transferRow, 0, len(userIDs))
	for start := 0; start < len(userIDs); start += transferChunk {
		chunk := userIDs[start:min(start+transferChunk, len(userIDs))]
		result, err := tx.QueryContext(ctx, `
SELECT id, service_id, status, admin_disabled_at IS NOT NULL OR service_limit_disabled_at IS NOT NULL
FROM users
WHERE status != ? AND id IN (`+placeholders(len(chunk))+`)`, append([]any{string(UserStatusDeleted)}, int64Args(chunk)...)...)
		if err != nil {
			return nil, err
		}
		for result.Next() {
			var row transferRow
			if err := result.Scan(&row.id, &row.serviceID, &row.status, &row.suspended); err != nil {
				result.Close()
				return nil, err
			}
			rows = append(rows, row)
		}
		if err := result.Close(); err != nil {
			return nil, err
		}
	}

	ids := make([]int64, 0, len(rows))
	restored := make([]int64, 0)
	// Active users each service gains, checked against destination's limits.
	gained := map[int64]int64{}
	var gainedTotal int64
	for _, row := range rows {
		ids = append(ids, row.id)
		if row.serviceID.Valid && !adminCanUseService(destination, row.serviceID.Int64) {
			return nil, clientError(403, fmt.Sprintf("%s does not have service %d. Give the service to this admin first.", destination.Username, row.serviceID.Int64))
		}
		restore := row.status == string(UserStatusDisabled) && row.suspended
		if restore {
			restored = append(restored, row.id)
		}
		if row.status == string(UserStatusActive) || restore {
			gained[row.serviceID.Int64] += 1
			gainedTotal++
		}
	}
	if err := ensureTransferCapacityTx(ctx, tx, destination, gained, gainedTotal); err != nil {
		return nil, err
	}

	for start := 0; start < len(ids); start += transferChunk {
		chunk := ids[start:min(start+transferChunk, len(ids))]
		if _, err := tx.ExecContext(ctx, `UPDATE users SET admin_id = ? WHERE id IN (`+placeholders(len(chunk))+`)`, append([]any{destination.ID}, int64Args(chunk)...)...); err != nil {
			return nil, err
		}
	}
	for start := 0; start < len(restored); start += transferChunk {
		chunk := restored[start:min(start+transferChunk, len(restored))]
		args := append([]any{dbTime(now), string(UserStatusOnHold), string(UserStatusActive), dbTime(now)}, int64Args(chunk)...)
		args = append(args, string(UserStatusDisabled))
		if _, err := tx.ExecContext(ctx, `
UPDATE users
SET status = CASE WHEN (on_hold_timeout IS NOT NULL AND on_hold_timeout > ?) OR COALESCE(on_hold_expire_duration, 0) > 0 THEN ? ELSE ? END,
    last_status_change = ?, admin_disabled_at = NULL, service_limit_disabled_at = NULL
WHERE id IN (`+placeholders(len(chunk))+`) AND status = ?`, args...); err != nil {
			return nil, err
		}
	}
	return restored, nil
}

func adminCanUseService(admin adminapp.Admin, serviceID int64) bool {
	if admin.Role == adminapp.RoleFullAccess || admin.Role == adminapp.RoleSudo {
		return true
	}
	return adminAssignedToService(admin, serviceID)
}

// ensureTransferCapacityTx applies the same active-user caps that creating
// or activating users does, so a transfer cannot push an admin past them.
func ensureTransferCapacityTx(ctx context.Context, tx *sql.Tx, destination adminapp.Admin, gained map[int64]int64, gainedTotal int64) error {
	if destination.Role == adminapp.RoleFullAccess || gainedTotal == 0 {
		return nil
	}
	if destination.UseServiceTrafficLimits {
		for serviceID, count := range gained {
			if serviceID == 0 {
				continue
			}
			id := serviceID
			limit := AdminServiceLimit(destination, &id)
			if limit == nil || limit.UsersLimit == nil || *limit.UsersLimit <= 0 {
				continue
			}
			var active int64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE admin_id = ? AND status = ? AND service_id = ?`, destination.ID, string(UserStatusActive), serviceID).Scan(&active); err != nil {
				return err
			}
			if active+count > *limit.UsersLimit {
				return clientError(400, fmt.Sprintf("%s can have at most %d active users on service %d; it has %d and would get %d more.", destination.Username, *limit.UsersLimit, serviceID, active, count))
			}
		}
		return nil
	}
	if destination.UsersLimit == nil || *destination.UsersLimit <= 0 {
		return nil
	}
	var active int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE admin_id = ? AND status = ?`, destination.ID, string(UserStatusActive)).Scan(&active); err != nil {
		return err
	}
	if active+gainedTotal > *destination.UsersLimit {
		return clientError(400, fmt.Sprintf("%s can have at most %d active users; it has %d and would get %d more.", destination.Username, *destination.UsersLimit, active, gainedTotal))
	}
	return nil
}
