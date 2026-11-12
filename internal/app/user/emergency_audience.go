package user

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// The emergency tunnel can be shown only to Hamrah Aval (MCI) users, whose
// network it is bought for. The API layer names the network a subscription
// request came from (SubscriptionRenderRequest.ClientOperator); this decides,
// per user, whether the emergency configs go into the subscription.

// emergencyInboundTags are the emergency tunnel's inbounds
// (xrayconfig.TSinEmergencyVMessTag and TSinEmergencySSTag) and the
// countries' tunnel inbounds (xrayconfig.TSinTunnelInboundTag, matched by the
// pattern): every config that goes through the Iranian tunnel, which the
// "Hamrah Aval only" choice is about.
var emergencyInboundTags = []string{"emergency-vmess", "emergency-ss", "tsin-*-tunnel"}

const (
	clientOperatorMCI     = "mci"
	clientOperatorIran    = "ir"
	clientOperatorForeign = "foreign"

	// emergencyMCIMemory is how long a user last seen on MCI keeps the
	// emergency configs when an update comes through a VPN.
	emergencyMCIMemory = 7 * 24 * time.Hour
	// mciSeenWriteEvery limits writes of mci_seen_at to one an hour per user.
	mciSeenWriteEvery = time.Hour
)

// showEmergencyFor decides for one subscription request:
//   - from MCI: shown, and remembered;
//   - from another Iranian network (Irancell, fixed lines...): hidden;
//   - from abroad, which is an update made through a VPN: shown only if the
//     user updated from MCI in the last week, so an MCI user keeps it;
//   - network unknown (the ranges are not loaded yet): shown, as before the
//     feature, rather than cutting MCI users off.
func (s Service) showEmergencyFor(ctx context.Context, userID int64, operator string) bool {
	switch operator {
	case clientOperatorMCI:
		s.repo.markMCISeen(ctx, userID)
		return true
	case clientOperatorIran:
		return false
	case clientOperatorForeign:
		seen, ok := s.repo.mciSeenAt(ctx, userID)
		return ok && time.Since(seen) < emergencyMCIMemory
	}
	return true
}

func (r Repository) mciSeenAt(ctx context.Context, userID int64) (time.Time, bool) {
	var seen sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT mci_seen_at FROM user_subscription_access WHERE user_id = ?`, userID).Scan(&seen)
	if err != nil || !seen.Valid {
		return time.Time{}, false
	}
	return seen.Time, true
}

// markMCISeen records that the user updated from MCI now. The access row
// exists by then: the subscription request wrote it first.
func (r Repository) markMCISeen(ctx context.Context, userID int64) {
	now := time.Now().UTC()
	r.cache.mu.Lock()
	if r.cache.mciSeen == nil {
		r.cache.mciSeen = map[int64]time.Time{}
	}
	if last, ok := r.cache.mciSeen[userID]; ok && now.Sub(last) < mciSeenWriteEvery {
		r.cache.mu.Unlock()
		return
	}
	r.cache.mciSeen[userID] = now
	r.cache.mu.Unlock()
	result, err := r.db.ExecContext(ctx, `UPDATE user_subscription_access SET mci_seen_at = ? WHERE user_id = ?`, dbTime(now), userID)
	if err == nil {
		if rows, rowsErr := result.RowsAffected(); rowsErr == nil && rows > 0 {
			return
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// Forget the write so the next request tries again.
		r.cache.mu.Lock()
		delete(r.cache.mciSeen, userID)
		r.cache.mu.Unlock()
	}
}

// hideHosts marks the hosts of the given inbounds as not shown, the same way
// a turned-off emergency tunnel hides them, so no config is built for them.
func hideHosts(hosts []Host, tags []string) []Host {
	if len(tags) == 0 {
		return hosts
	}
	hidden := make(map[string]bool, len(tags))
	var patterns [][2]string
	for _, tag := range tags {
		// One "*" in a tag matches anything between its two ends.
		if prefix, suffix, ok := strings.Cut(tag, "*"); ok {
			patterns = append(patterns, [2]string{prefix, suffix})
			continue
		}
		hidden[tag] = true
	}
	matches := func(tag string) bool {
		if hidden[tag] {
			return true
		}
		for _, pattern := range patterns {
			if len(tag) >= len(pattern[0])+len(pattern[1]) && strings.HasPrefix(tag, pattern[0]) && strings.HasSuffix(tag, pattern[1]) {
				return true
			}
		}
		return false
	}
	result := make([]Host, len(hosts))
	copy(result, hosts)
	for i := range result {
		if matches(result[i].InboundTag) {
			result[i].IsDisabled = true
			result[i].EnabledAdmins = nil
		}
	}
	return result
}
