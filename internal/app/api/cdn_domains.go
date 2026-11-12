package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The Cloudflare domain pool keeps several domains set up for the countries
// in path mode. One is in use (tsin_settings.gate_host); the others wait
// ready, each checked from outside Iran (a WebSocket handshake through
// Cloudflare to the gate, the same test as the setup checklist) and from
// Iran's test servers (an HTTPS fetch, which a blocked domain name does not
// get). When the domain in use is blocked, another is put in its place with
// one press: only the configs' address changes, nothing restarts, and users
// get it when they update their subscription.

const (
	cdnDomainWatchEvery = 30 * time.Minute
	cdnDomainIranEvery  = 3 * time.Hour
	cdnDomainWatchFirst = 5 * time.Minute
)

type cdnDomain struct {
	ID            int64     `json:"id"`
	Domain        string    `json:"domain"`
	Note          string    `json:"note"`
	Active        bool      `json:"active"`
	CheckedAt     *string   `json:"checked_at,omitempty"`
	CheckState    string    `json:"check_state"`
	CheckReason   string    `json:"check_reason"`
	CheckStatus   int       `json:"check_status"`
	CheckDetail   string    `json:"check_detail,omitempty"`
	IranCheckedAt *string   `json:"iran_checked_at,omitempty"`
	IranOpen      int       `json:"iran_open"`
	IranTotal     int       `json:"iran_total"`
	IranError     string    `json:"iran_error,omitempty"`
	createdAt     time.Time `json:"-"`
}

// cdnDomainWatchState remembers what the watch last told the admin about the
// domain in use, so each change is reported once.
type cdnDomainWatchState struct {
	mu          sync.Mutex
	failures    int
	downAlerted bool
	iranBlocked bool
	lastIranAt  time.Time
}

var cdnDomainWatch cdnDomainWatchState

func formatOptionalTime(value sql.NullTime) *string {
	if !value.Valid {
		return nil
	}
	text := value.Time.UTC().Format(time.RFC3339)
	return &text
}

// listCDNDomains returns the pool, with the domain in use first. The domain
// in use joins the pool the first time it is read, so the pool never misses
// the one users have now.
func (s *Server) listCDNDomains(ctx context.Context, active string) ([]cdnDomain, error) {
	active = strings.ToLower(strings.TrimSpace(active))
	if active != "" {
		if _, err := s.db.ExecContext(ctx, s.insertIgnoreCDNDomain(), active, "", time.Now().UTC()); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, domain, COALESCE(note, ''), checked_at, check_state, check_reason,
check_status, COALESCE(check_detail, ''), iran_checked_at, iran_open, iran_total, COALESCE(iran_error, '')
FROM cdn_domains ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []cdnDomain
	for rows.Next() {
		var item cdnDomain
		var checkedAt, iranAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.Domain, &item.Note, &checkedAt, &item.CheckState, &item.CheckReason,
			&item.CheckStatus, &item.CheckDetail, &iranAt, &item.IranOpen, &item.IranTotal, &item.IranError); err != nil {
			return nil, err
		}
		item.CheckedAt = formatOptionalTime(checkedAt)
		item.IranCheckedAt = formatOptionalTime(iranAt)
		item.Active = item.Domain == active
		domains = append(domains, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(domains, func(i, j int) bool { return domains[i].Active && !domains[j].Active })
	return domains, nil
}

func (s *Server) insertIgnoreCDNDomain() string {
	if strings.Contains(strings.ToLower(s.dialect), "mysql") || strings.Contains(strings.ToLower(s.dialect), "maria") {
		return `INSERT IGNORE INTO cdn_domains (domain, note, created_at) VALUES (?, ?, ?)`
	}
	return `INSERT INTO cdn_domains (domain, note, created_at) VALUES (?, ?, ?) ON CONFLICT(domain) DO NOTHING`
}

func (s *Server) cdnDomainByID(ctx context.Context, id int64) (string, error) {
	var domain string
	err := s.db.QueryRowContext(ctx, `SELECT domain FROM cdn_domains WHERE id = ?`, id).Scan(&domain)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("that domain is not in the list")
	}
	return domain, err
}

// checkCDNDomain sends a WebSocket handshake through Cloudflare to the gate
// on one country's path, from this server (outside Iran), and stores what it
// found. ok means Cloudflare, the domain's DNS record, its SSL mode, Origin
// Rule and security rule are all set up for it.
func (s *Server) checkCDNDomain(ctx context.Context, domain, path string) (state, reason string, status int, detail string) {
	switch {
	case path == "":
		state, reason = "fail", "no_path"
	default:
		if _, err := net.DefaultResolver.LookupHost(ctx, domain); err != nil {
			state, reason, detail = "fail", "dns", err.Error()
			break
		}
		response, err := tsinProbe(ctx, http.MethodGet, "https://"+domain+path, map[string]string{
			"Connection": "Upgrade", "Upgrade": "websocket", "Sec-WebSocket-Version": "13",
			"Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
		})
		if err != nil {
			state, reason, detail = "fail", "unreachable", err.Error()
			break
		}
		status = response.StatusCode
		state, reason = tsinGateVerdict(response.StatusCode, response.Header)
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE cdn_domains SET checked_at = ?, check_state = ?, check_reason = ?, check_status = ?, check_detail = ? WHERE domain = ?`,
		time.Now().UTC(), state, reason, status, truncateText(detail, 500), domain)
	return state, reason, status, detail
}

// checkCDNDomainsFromIran has Iran's test servers download a file of several
// hundred kilobytes through each domain and stores, per domain, how many
// downloaded it in full (iran_open) out of those that downloaded it from a
// known Cloudflare site (iran_total). A plain fetch is not enough: Iran lets a
// new domain answer and then stalls it after a few tens of kilobytes, so the
// ping works and nothing loads. The test servers sit in datacenters, so this
// sees filtering nationwide, not a single mobile operator's.
func (s *Server) checkCDNDomainsFromIran(ctx context.Context, domains []string) (map[string]iranDownload, error) {
	results, err := checkCloudflareDownloadsFromIran(ctx, http.DefaultClient, checkHostBaseURL, domains)
	now := time.Now().UTC()
	for _, domain := range domains {
		result, ok := results[domain]
		message := ""
		switch {
		case err != nil:
			message = err.Error()
		case !ok:
			message = "the check from Iran did not finish"
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE cdn_domains SET iran_checked_at = ?, iran_open = ?, iran_total = ?, iran_error = ? WHERE domain = ?`,
			now, result.Complete, result.Total, truncateText(message, 500), domain)
	}
	return results, err
}

func (s *Server) checkCDNDomainFromIran(ctx context.Context, domain string) (iranDownload, error) {
	results, err := s.checkCDNDomainsFromIran(ctx, []string{domain})
	if err != nil {
		return iranDownload{}, err
	}
	result, ok := results[domain]
	if !ok {
		return iranDownload{}, fmt.Errorf("the check from Iran did not finish")
	}
	return result, nil
}

// iranFast is a domain at least half of Iran's test servers downloaded
// through in full; iranSlow is one at most a quarter did.
func iranFast(open, total int) bool { return total >= cloudflareDownloadMinimum && open*2 >= total }
func iranSlow(open, total int) bool { return total >= cloudflareDownloadMinimum && open*4 <= total }

func truncateText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// cdnDomainsView is what the dashboard shows: the pool, the domain in use,
// and the values to enter in Cloudflare for every domain at once.
func (s *Server) cdnDomainsView(ctx context.Context) (map[string]any, error) {
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		return nil, err
	}
	domains, err := s.listCDNDomains(ctx, settings.GateHost)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(domains))
	for _, item := range domains {
		names = append(names, `"`+item.Domain+`"`)
	}
	expression := ""
	if len(names) > 0 {
		expression = "(http.host in {" + strings.Join(names, " ") + "})"
	}
	return map[string]any{
		"path_mode":   settings.Transport == xrayconfig.TSinTransportPath && settings.LastAppliedAt.Valid,
		"active":      strings.ToLower(strings.TrimSpace(settings.GateHost)),
		"origin_ip":   s.tsinTargetAddress(ctx, settings.Target),
		"origin_port": settings.GatePort,
		"expression":  expression,
		"domains":     domains,
	}, nil
}

// handleCoreCDNDomains lists the pool (GET) or acts on it (POST with action
// add, remove, check, iran or activate).
func (s *Server) handleCoreCDNDomains(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		view, err := s.cdnDomainsView(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, view)
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
		Action string `json:"action"`
		ID     int64  `json:"id"`
		Domain string `json:"domain"`
		Note   string `json:"note"`
		// Force switches to a domain whose check from outside fails.
		Force bool `json:"force"`
	}
	if err := decodeOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	settings, err := s.loadTSinSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	active := strings.ToLower(strings.TrimSpace(settings.GateHost))
	result := map[string]any{}

	switch request.Action {
	case "add":
		domain, err := xrayconfig.NormalizeCDNDomain(request.Domain)
		if err != nil || domain == "" {
			writeError(w, http.StatusUnprocessableEntity, "enter a plain domain such as g2.example.com, without https:// or a path")
			return
		}
		if _, err := s.db.ExecContext(ctx, s.insertIgnoreCDNDomain(), domain, strings.TrimSpace(request.Note), time.Now().UTC()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if settings.Transport == xrayconfig.TSinTransportPath {
			state, reason, status, _ := s.checkCDNDomain(ctx, domain, s.tsinCountryPath(ctx))
			result["check"] = map[string]any{"state": state, "reason": reason, "status": status}
		}
	case "remove":
		domain, err := s.cdnDomainByID(ctx, request.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if domain == active {
			writeError(w, http.StatusUnprocessableEntity, "this domain is in use; put another one in use first")
			return
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM cdn_domains WHERE id = ?`, request.ID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "check":
		domain, err := s.cdnDomainByID(ctx, request.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		state, reason, status, _ := s.checkCDNDomain(ctx, domain, s.tsinCountryPath(ctx))
		result["check"] = map[string]any{"state": state, "reason": reason, "status": status}
	case "iran":
		domain, err := s.cdnDomainByID(ctx, request.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		download, err := s.checkCDNDomainFromIran(ctx, domain)
		if err != nil {
			writeError(w, http.StatusBadGateway, "the check from Iran could not run: "+err.Error())
			return
		}
		result["iran"] = map[string]any{"open": download.Complete, "total": download.Total,
			"reached": download.Reached, "throttled": download.throttled()}
	case "activate":
		if settings.Transport != xrayconfig.TSinTransportPath || !settings.LastAppliedAt.Valid {
			writeError(w, http.StatusUnprocessableEntity, "the countries are not served through Cloudflare (path mode) yet")
			return
		}
		domain, err := s.cdnDomainByID(ctx, request.ID)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if domain == active {
			break
		}
		if !request.Force {
			// Never move users onto a domain that does not work: check it
			// now, through Cloudflare, before touching any config.
			if state, reason, status, _ := s.checkCDNDomain(ctx, domain, s.tsinCountryPath(ctx)); state != "ok" {
				writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
					"detail": "this domain does not work through Cloudflare yet, so users were not moved to it",
					"check":  map[string]any{"state": state, "reason": reason, "status": status},
				})
				return
			}
		}
		if !tsinApplyMu.TryLock() {
			writeError(w, http.StatusConflict, "another T.Sin change is still running")
			return
		}
		moved, err := s.configRepo.SetTSinGateHost(ctx, active, domain)
		if err == nil {
			settings.GateHost = domain
			err = s.saveTSinSettings(ctx, settings)
		}
		tsinApplyMu.Unlock()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cdnDomainWatch.mu.Lock()
		cdnDomainWatch.failures, cdnDomainWatch.downAlerted, cdnDomainWatch.iranBlocked = 0, false, false
		cdnDomainWatch.mu.Unlock()
		logging.Infof(logging.ComponentNode, "Cloudflare domain switched from %s to %s (%d configs)", active, domain, moved)
		result["moved"] = moved
	default:
		writeError(w, http.StatusBadRequest, "send action: add, remove, check, iran or activate")
		return
	}

	view, err := s.cdnDomainsView(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for key, value := range result {
		view[key] = value
	}
	writeJSON(w, http.StatusOK, view)
}

// runCDNDomainWatch checks every domain in the pool from outside every half
// hour and the domain in use from Iran every three hours, and tells the admin
// on Telegram when the domain in use stops working and when it works again.
func (s *Server) runCDNDomainWatch(ctx context.Context) {
	timer := time.NewTimer(cdnDomainWatchFirst)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.checkCDNDomains(ctx)
			timer.Reset(cdnDomainWatchEvery)
		}
	}
}

func (s *Server) checkCDNDomains(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Warnf(logging.ComponentNode, "Cloudflare domain check failed: %v", recovered)
		}
	}()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	settings, err := s.loadTSinSettings(checkCtx)
	if err != nil || settings.Transport != xrayconfig.TSinTransportPath || !settings.LastAppliedAt.Valid {
		return
	}
	domains, err := s.listCDNDomains(checkCtx, settings.GateHost)
	if err != nil {
		return
	}
	path := s.tsinCountryPath(checkCtx)
	alert := func(title string, lines ...string) {
		s.telegramReports.TSinTunnel(context.WithoutCancel(ctx), title, lines...)
	}
	ready := map[string]bool{}
	for _, item := range domains {
		state, reason, status, _ := s.checkCDNDomain(checkCtx, item.Domain, path)
		if !item.Active {
			ready[item.Domain] = state == "ok"
			continue
		}
		st := &cdnDomainWatch
		st.mu.Lock()
		if state == "ok" {
			wasDown := st.downAlerted
			st.failures, st.downAlerted = 0, false
			st.mu.Unlock()
			if wasDown {
				alert("دامنهٔ Cloudflare دوباره کار می‌کند", item.Domain)
			}
		} else {
			st.failures++
			// Two misses in a row, an hour apart at most, before telling
			// anyone: one failed handshake is often just a slow moment.
			report := st.failures >= 2 && !st.downAlerted
			if report {
				st.downAlerted = true
			}
			st.mu.Unlock()
			if report {
				alert("دامنهٔ Cloudflare در حال استفاده از بیرون جواب نمی‌دهد",
					item.Domain, fmt.Sprintf("علت: %s (کد %d)", reason, status),
					"در داشبورد بخش «دامنه‌های Cloudflare» را باز کن؛ اگر دامنهٔ آماده داری، آن را «در حال استفاده» کن.")
			}
		}
	}
	// From Iran, every three hours, every domain in the pool: a download of
	// a few hundred kilobytes through each, so a domain that answers a ping
	// but stalls (a filtered new domain) shows as slow, and a spare that comes
	// free is reported.
	st := &cdnDomainWatch
	st.mu.Lock()
	due := time.Since(st.lastIranAt) >= cdnDomainIranEvery
	if due {
		st.lastIranAt = time.Now()
	}
	st.mu.Unlock()
	active := strings.ToLower(strings.TrimSpace(settings.GateHost))
	if !due || len(domains) == 0 {
		return
	}
	names := make([]string, 0, len(domains))
	for _, item := range domains {
		names = append(names, item.Domain)
	}
	results, err := s.checkCDNDomainsFromIran(checkCtx, names)
	if err != nil {
		return
	}
	countsOf := func(result iranDownload) string {
		return fmt.Sprintf("از %d سرور تست در دیتاسنترهای ایران، %d سرور یک فایل چندصدکیلوبایتی را از این دامنه کامل دانلود کردند.", result.Total, result.Complete)
	}
	fastSpares := []string{}
	for _, item := range domains {
		result, ok := results[item.Domain]
		if !ok || item.Domain == active {
			continue
		}
		if !ready[item.Domain] {
			continue
		}
		if iranFast(result.Complete, result.Total) {
			fastSpares = append(fastSpares, item.Domain)
		}
		// A spare that was slow from Iran at its last check and is fast now.
		wasSlow := item.IranCheckedAt != nil && item.IranError == "" && iranSlow(item.IranOpen, item.IranTotal)
		if wasSlow && iranFast(result.Complete, result.Total) {
			alert("یک دامنهٔ آماده از ایران آزاد شد و سرعت دارد", item.Domain, countsOf(result),
				"اگر دامنهٔ در حال استفاده کند است، در داشبورد بخش «دامنه‌های Cloudflare» این دامنه را «در حال استفاده» کن تا کاربرها با آپدیت ساب رویش بروند.")
		}
	}
	result, ok := results[active]
	if active == "" || !ok {
		return
	}
	st.mu.Lock()
	wasBlocked := st.iranBlocked
	switch {
	case iranSlow(result.Complete, result.Total):
		st.iranBlocked = true
	case iranFast(result.Complete, result.Total):
		st.iranBlocked = false
	}
	nowBlocked := st.iranBlocked
	st.mu.Unlock()
	if nowBlocked && !wasBlocked {
		next := "دامنهٔ آماده‌ای که از ایران سرعت داشته باشد در لیست نیست؛ تا آن موقع تونل اضطراری را روشن نگه دار."
		if len(fastSpares) > 0 {
			next = "این دامنهٔ آماده از ایران سرعت دارد؛ آن را «در حال استفاده» کن تا کاربرها با آپدیت ساب رویش بروند: " + strings.Join(fastSpares, "، ")
		}
		what := "دانلود از ایران کامل نمی‌شود: ممکن است پینگ داشته باشد ولی سرعت ندارد؛ احتمالاً فیلتر یا خفه شده."
		alert("دامنهٔ Cloudflare در حال استفاده از ایران سرعت ندارد", active, what, countsOf(result), next)
	} else if !nowBlocked && wasBlocked {
		alert("دامنهٔ Cloudflare در حال استفاده دوباره از ایران سرعت دارد", active, countsOf(result))
	}
}
