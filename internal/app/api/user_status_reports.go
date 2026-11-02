package api

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	telegramapp "github.com/aliking1367/next/internal/app/telegram"
)

const (
	userStatusReportInterval = 30 * time.Second
	// userStatusReportOverlap re-reads a little of the previous window, since
	// some databases keep last_status_change to the second only.
	userStatusReportOverlap = time.Minute
	// userStatusReportBatch keeps a wave of expiring users from flooding the
	// Telegram chat in one go (about 20 a minute, under Telegram's group
	// limit); the rest follow on the next ticks.
	userStatusReportBatch = 10
)

// runUserStatusReportWorker reports users that the panel limited or expired
// on its own. The usage flush and the lifecycle review change the status
// directly in the database, so without this only an admin's manual change
// ever reached Telegram and the webhooks.
func (s *Server) runUserStatusReportWorker(ctx context.Context) {
	if s.db == nil {
		return
	}
	since := time.Now().UTC()
	seen := map[string]time.Time{}
	ticker := time.NewTicker(userStatusReportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var reports []telegramapp.UserReport
			reports, since = s.automaticStatusChanges(ctx, since, seen)
			for _, report := range reports {
				s.telegramReports.UserStatusChanged(ctx, report)
				s.enqueueWebhook(ctx, webhookUserEvent(webhookUserStatusAction(report.Status), report))
			}
		}
	}
}

type automaticStatusChange struct {
	id        int64
	username  string
	status    string
	owner     sql.NullString
	dataLimit sql.NullInt64
	expire    sql.NullInt64
	changedAt string
}

// automaticStatusChanges returns the users limited or expired since the last
// tick that were not reported yet, and where the next tick should read from.
func (s *Server) automaticStatusChanges(ctx context.Context, since time.Time, seen map[string]time.Time) ([]telegramapp.UserReport, time.Time) {
	tickStart := time.Now().UTC()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(queryCtx, `
SELECT u.id, u.username, u.status, a.username, u.data_limit, u.expire, u.last_status_change
FROM users u
LEFT JOIN admins a ON a.id = u.admin_id
WHERE u.status IN ('limited', 'expired')
  AND u.last_status_change >= ?
ORDER BY u.last_status_change, u.id`,
		since.Add(-userStatusReportOverlap).Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		logging.Warnf(logging.ComponentUser, "status report query failed: %v", err)
		return nil, since
	}
	changes := make([]automaticStatusChange, 0)
	for rows.Next() {
		var item automaticStatusChange
		var changedAt any
		if err := rows.Scan(&item.id, &item.username, &item.status, &item.owner, &item.dataLimit, &item.expire, &changedAt); err != nil {
			rows.Close()
			logging.Warnf(logging.ComponentUser, "status report scan failed: %v", err)
			return nil, since
		}
		item.changedAt = fmt.Sprint(changedAt)
		changes = append(changes, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		logging.Warnf(logging.ComponentUser, "status report read failed: %v", err)
		return nil, since
	}

	reports := make([]telegramapp.UserReport, 0)
	for _, item := range changes {
		key := fmt.Sprintf("%d:%s:%s", item.id, item.status, item.changedAt)
		if _, ok := seen[key]; ok {
			continue
		}
		if len(reports) == userStatusReportBatch {
			// Keep the window where it is so the rest are read again.
			return reports, since
		}
		seen[key] = tickStart
		report := telegramapp.UserReport{
			Username: item.username,
			Owner:    item.owner.String,
			Actor:    "system",
			Status:   item.status,
		}
		if item.dataLimit.Valid {
			report.DataLimit = &item.dataLimit.Int64
		}
		if item.expire.Valid {
			report.Expire = &item.expire.Int64
		}
		reports = append(reports, report)
	}
	for key, at := range seen {
		if tickStart.Sub(at) > 3*userStatusReportOverlap {
			delete(seen, key)
		}
	}
	return reports, tickStart
}
