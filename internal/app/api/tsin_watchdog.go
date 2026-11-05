package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/logging"
	"github.com/aliking1367/next/internal/app/tsin"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The T.Sin watchdog keeps the tunnel on this panel's own machine alive, the
// way T.Sin's own client does with its failover service: a tunnel that has
// been down for two checks in a row is restarted, then registered again on
// its datacenter, and only if that datacenter no longer answers is it moved
// to the fastest one that serves the same countries on the same ports.
//
// It never touches a healthy tunnel, never runs during an Apply, and never
// changes a config: a datacenter that would need different ports is only
// reported, because moving there means the admin applies it on purpose.
// Countries served from a node have their tunnel there; the node script
// installs the same watch on that machine.

const (
	tsinWatchInterval = time.Minute
	// tsinWatchDownAfter is how many failed checks in a row count as down.
	tsinWatchDownAfter = 2
	// tsinWatchRetryEvery spaces repair attempts while it stays down.
	tsinWatchRetryEvery = 5
	// tsinHandshakeFresh is how old a handshake may be on a working tunnel;
	// WireGuard renews it every two minutes while traffic or keepalives flow.
	tsinHandshakeFresh = 3 * time.Minute
	// tsinPortsRefresh is how often a healthy tunnel's country ports are read
	// again, to know what a replacement datacenter must match.
	tsinPortsRefresh = 10 * time.Minute
	// tsinAccountEvery is how often the account itself is looked at, and
	// tsinAlertRepeat how often a warning that still holds is sent again.
	tsinAccountEvery = 30 * time.Minute
	tsinAlertRepeat  = 24 * time.Hour
)

// tsinRepairWait is how long a repair step waits for a handshake. Tests
// shorten it.
var tsinRepairWait = 20 * time.Second

// tsinReleaseWait is the pause between ending a stale session and
// registering again. Tests shorten it.
var tsinReleaseWait = 5 * time.Second

// tsinWatch is the watchdog's memory between checks.
type tsinWatch struct {
	tunnel tsin.Tunnel
	client tsin.Client

	failures   int
	down       bool
	ports      map[string]int
	portsAt    time.Time
	lastReport string

	accountAt time.Time
	alerted   map[string]time.Time

	// What the reports from a node said last; see checkTSinNode.
	nodeReportAt    time.Time
	nodeSilent      bool
	nodeDown        bool
	nodeDownReports int
	nodeDownSince   time.Time
	nodeFailing     string
	nodeFailingAt   time.Time
}

func (s *Server) runTSinWatchdog(ctx context.Context) {
	ticker := time.NewTicker(tsinWatchInterval)
	defer ticker.Stop()
	watch := &tsinWatch{tunnel: tsin.NewTunnel(), client: tsin.NewClient()}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkTSinTunnel(ctx, watch)
		}
	}
}

// checkTSinTunnel runs one check. A failure in it is logged and the next
// check starts afresh; the panel and its users never depend on it.
func (s *Server) checkTSinTunnel(ctx context.Context, watch *tsinWatch) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Warnf(logging.ComponentTelegram, "T.Sin watchdog check failed: %v", recovered)
		}
	}()
	if !tsinApplyMu.TryLock() {
		return // an Apply is running and owns the tunnel right now
	}
	defer tsinApplyMu.Unlock()

	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	tunnel, client := watch.tunnel, watch.client
	settings, err := s.loadTSinSettings(checkCtx)
	if err != nil {
		return // try again next minute with what it already knows
	}
	if tsinAccountWatched(settings) && time.Since(watch.accountAt) > tsinAccountEvery {
		watch.accountAt = time.Now()
		s.checkTSinAccount(checkCtx, settings, client, watch)
	}
	if settings.Target != xrayconfig.MasterTargetID && tsinAccountWatched(settings) {
		s.checkTSinNode(ctx, settings, watch)
	}
	if !tsinWatched(settings) || !tunnel.ConfigExists() {
		watch.reset()
		return
	}

	if tsinTunnelHealthy(checkCtx, tunnel) {
		if watch.down {
			s.telegramReports.TSinTunnel(ctx, "تونل T.Sin دوباره وصل شد", "دیتاسنتر: "+settings.RegisteredDC)
		}
		watch.failures, watch.down = 0, false
		if time.Since(watch.portsAt) > tsinPortsRefresh {
			if status, err := client.Status(checkCtx, settings.RegisteredDC, settings.Token); err == nil {
				watch.ports, watch.portsAt = status.PortsOf(settings.Countries), time.Now()
			}
		}
		return
	}

	watch.failures++
	if watch.failures < tsinWatchDownAfter {
		return
	}
	if (watch.failures-tsinWatchDownAfter)%tsinWatchRetryEvery != 0 {
		return
	}
	if !watch.down {
		watch.down = true
		s.telegramReports.TSinTunnel(ctx, "تونل T.Sin قطع است؛ در حال تعمیر", "دیتاسنتر: "+settings.RegisteredDC)
	}
	report := s.repairTSinTunnel(checkCtx, &settings, tunnel, client, watch)
	logging.Warnf(logging.ComponentTelegram, "T.Sin watchdog: %s", report)
	if report != watch.lastReport && !strings.HasPrefix(report, "fixed") {
		lines := []string{report}
		if explained := tsin.ExplainFA(tsin.ClassifyText(report)); explained != "" {
			lines = append(lines, explained)
		}
		s.telegramReports.TSinTunnel(ctx, "تونل T.Sin هنوز وصل نشده", lines...)
	}
	watch.lastReport = report
}

// reset forgets an incident, for when there is nothing to watch.
func (w *tsinWatch) reset() {
	w.failures, w.down, w.ports, w.portsAt, w.lastReport = 0, false, nil, time.Time{}, ""
}

// tsinAccountWatched reports whether there is an account in use to look at,
// wherever its tunnel runs.
func tsinAccountWatched(settings tsinSettings) bool {
	return tsin.ValidToken(settings.Token) && settings.LastAppliedAt.Valid && len(settings.Countries) > 0
}

// checkTSinAccount warns before the account stops serving the countries:
// traffic nearly or fully used, expiry close or passed, the account switched
// off, or a chosen country no longer offered. It only reads; each warning is
// repeated once a day while it holds.
func (s *Server) checkTSinAccount(ctx context.Context, settings tsinSettings, client tsin.Client, watch *tsinWatch) {
	var status tsin.Status
	var err error
	if report := s.tsinNode.load(settings.Target); report.fresh() && len(report.Account) > 0 {
		// T.Sin shows the account to the node holding the session, not here.
		status, err = tsin.ParseStatus(report.Account)
	} else {
		dc := settings.RegisteredDC
		if !tsin.ValidDC(dc) {
			dc = settings.DC
		}
		status, err = client.Status(ctx, dc, settings.Token)
	}
	if err != nil {
		return // the tunnel check reports an unreachable datacenter
	}
	for key, alert := range tsinAccountAlerts(status, settings.Countries, time.Now()) {
		if alert == nil {
			delete(watch.alerted, key)
			continue
		}
		if when, ok := watch.alerted[key]; ok && time.Since(when) < tsinAlertRepeat {
			continue
		}
		if watch.alerted == nil {
			watch.alerted = map[string]time.Time{}
		}
		watch.alerted[key] = time.Now()
		s.telegramReports.TSinTunnel(context.WithoutCancel(ctx), alert[0], alert[1:]...)
	}
}

// tsinAccountAlerts says, for each kind of warning, what to send now; nil
// means the condition does not hold.
func tsinAccountAlerts(status tsin.Status, countries []string, now time.Time) map[string][]string {
	alerts := map[string][]string{"data": nil, "expiry": nil, "inactive": nil, "missing": nil}
	if status.DataLimitGB > 0 {
		used := status.DataUsedGB / status.DataLimitGB
		usage := fmt.Sprintf("مصرف: %.1f از %.0f گیگ (%.0f٪)", status.DataUsedGB, status.DataLimitGB, used*100)
		switch {
		case used >= 1:
			alerts["data"] = []string{"حجم اکانت T.Sin تمام شد", usage, "کشورها تا شارژ دوباره کار نمی‌کنند."}
		case used >= 0.9:
			alerts["data"] = []string{"حجم اکانت T.Sin رو به اتمام است", usage}
		}
	}
	if expiry, ok := status.Expiry(); ok {
		left := expiry.Sub(now)
		switch {
		case left <= 0:
			alerts["expiry"] = []string{"اکانت T.Sin منقضی شد", "کشورها تا تمدید کار نمی‌کنند."}
		case left <= 3*24*time.Hour:
			alerts["expiry"] = []string{"اکانت T.Sin به‌زودی تمام می‌شود", fmt.Sprintf("حدود %.0f ساعت مانده؛ تمدیدش کن.", left.Hours())}
		}
	}
	if status.ActiveKnown && !status.Active {
		alerts["inactive"] = []string{"اکانت T.Sin غیرفعال است", "کشورها تا فعال شدن دوباره کار نمی‌کنند."}
	}
	if missing := status.Missing(countries); len(missing) > 0 {
		alerts["missing"] = []string{"این کشورها دیگر در اکانت T.Sin نیستند", strings.Join(missing, "، "), "کانفیگ این کشورها کار نمی‌کند تا T.Sin برشان گرداند."}
	}
	return alerts
}

// tsinWatched reports whether there is a tunnel on this machine to look after.
func tsinWatched(settings tsinSettings) bool {
	return tsin.ValidToken(settings.Token) &&
		tsin.ValidDC(settings.RegisteredDC) &&
		settings.Target == xrayconfig.MasterTargetID &&
		settings.LastAppliedAt.Valid &&
		len(settings.Countries) > 0
}

func tsinTunnelHealthy(ctx context.Context, tunnel tsin.Tunnel) bool {
	when, ok := tunnel.Handshake(ctx)
	return ok && time.Since(when) < tsinHandshakeFresh
}

// repairTSinTunnel tries, in order: restart, register again on the same
// datacenter, move to a datacenter with the same ports. It returns what
// happened in a line for the log and the admin.
func (s *Server) repairTSinTunnel(ctx context.Context, settings *tsinSettings, tunnel tsin.Tunnel, client tsin.Client, watch *tsinWatch) string {
	if err := tunnel.Restart(ctx); err == nil && tunnel.WaitHandshake(ctx, tsinRepairWait) {
		return "fixed by restarting the tunnel"
	}

	current := settings.RegisteredDC
	_, statusErr := client.Status(ctx, current, settings.Token)
	if statusErr == nil {
		err := s.registerTSinTunnel(ctx, settings, tunnel, client, current)
		if tsin.InUse(err) {
			// T.Sin still counts this machine's dead session as online and
			// refuses until it drops it on its side, which took about ten
			// minutes on 2026-11-05. The tunnel here is down, so ending that
			// session loses nothing.
			_ = client.Disconnect(ctx, current, settings.Token)
			select {
			case <-ctx.Done():
			case <-time.After(tsinReleaseWait):
			}
			if err = s.registerTSinTunnel(ctx, settings, tunnel, client, current); err == nil {
				return "fixed by ending the stale session and registering again on " + current
			}
		}
		if err == nil {
			return "fixed by registering again on " + current
		} else if tunnel.WaitHandshake(ctx, time.Second) {
			return "fixed"
		} else {
			return fmt.Sprintf("%s answers but registering again failed: %v", current, err)
		}
	}

	// The datacenter itself is gone. Look for one that can take over as is.
	if len(watch.ports) == 0 {
		return fmt.Sprintf("%s does not answer (%v); no record of the country ports yet, so no other datacenter is tried", current, statusErr)
	}
	ranked := client.RankDatacenters(ctx, settings.Token)
	var different []string
	for _, candidate := range ranked {
		if !candidate.OK || candidate.DC == current {
			continue
		}
		if !candidate.Status.SamePorts(watch.ports) {
			different = append(different, candidate.DC)
			continue
		}
		_ = client.Disconnect(ctx, current, settings.Token)
		if err := s.registerTSinTunnel(ctx, settings, tunnel, client, candidate.DC); err != nil {
			continue
		}
		settings.DC = candidate.DC
		if err := s.saveTSinSettings(ctx, *settings); err != nil {
			return "moved to " + candidate.DC + " but could not save it: " + err.Error()
		}
		s.telegramReports.TSinTunnel(context.WithoutCancel(ctx), "تونل T.Sin به دیتاسنتر دیگری رفت", current+" ← "+candidate.DC)
		return "fixed by moving to " + candidate.DC
	}
	if len(different) > 0 {
		return fmt.Sprintf("%s does not answer; %s work but serve the countries on other ports — choose one in the T.Sin dialog and Apply (users' links stay the same)", current, strings.Join(different, ", "))
	}
	return fmt.Sprintf("%s does not answer and no other datacenter accepts the token", current)
}

// registerTSinTunnel registers on dc and brings the tunnel up with the new
// profile. A profile whose proxy address differs from the one the configs use
// is not kept, since the configs would then point at nothing.
func (s *Server) registerTSinTunnel(ctx context.Context, settings *tsinSettings, tunnel tsin.Tunnel, client tsin.Client, dc string) error {
	previous, _ := tunnel.ReadConfig()
	registration, err := client.Register(ctx, dc, settings.Token)
	if err != nil {
		return err
	}
	conf, err := tsin.BuildConfig(registration)
	if err != nil {
		return err
	}
	if previous != "" && tsin.ProxyAddress(previous) != tsin.ProxyAddress(conf) {
		return fmt.Errorf("%s hands out proxy address %s, the configs use %s", dc, tsin.ProxyAddress(conf), tsin.ProxyAddress(previous))
	}
	if err := tunnel.Up(ctx, conf); err != nil {
		return err
	}
	if !tunnel.WaitHandshake(ctx, tsinRepairWait) {
		return fmt.Errorf("registered on %s but no handshake yet", dc)
	}
	settings.RegisteredDC, settings.RegisteredTokenSum = dc, tokenSum(settings.Token)
	return s.saveTSinSettings(ctx, *settings)
}

// tsinFailingRepeat spaces out alerts about countries that fail while the
// tunnel works, since one country can come and go.
const tsinFailingRepeat = 30 * time.Minute

// checkTSinNode follows a tunnel on a node through the reports the node sends.
// The node's own watch repairs it; the panel only tells the admin, with where
// the problem is.
func (s *Server) checkTSinNode(ctx context.Context, settings tsinSettings, watch *tsinWatch) {
	report := s.tsinNode.load(settings.Target)
	if report == nil {
		return // a node that never reported runs an older script
	}
	alert := func(title string, lines ...string) {
		s.telegramReports.TSinTunnel(context.WithoutCancel(ctx), title, lines...)
	}
	if !report.fresh() {
		if !watch.nodeSilent {
			watch.nodeSilent = true
			alert("نود دیگر وضعیت T.Sin را گزارش نمی‌دهد",
				fmt.Sprintf("آخرین گزارش: %.0f دقیقه پیش", time.Since(report.ReceivedAt).Minutes()),
				"یا خود نود خاموش است یا اینترنتش قطع است، یا next-tsin-watch روی آن متوقف شده.")
		}
		return
	}
	if watch.nodeSilent {
		watch.nodeSilent = false
		alert("نود دوباره وضعیت T.Sin را گزارش می‌دهد")
	}
	if !report.ReceivedAt.After(watch.nodeReportAt) {
		return // nothing new since the last check
	}
	watch.nodeReportAt = report.ReceivedAt

	if report.tunnelUp() {
		if watch.nodeDown {
			alert("تونل T.Sin روی نود دوباره وصل شد",
				fmt.Sprintf("مدت قطعی: حدود %.0f دقیقه", time.Since(watch.nodeDownSince).Minutes()+2))
		}
		watch.nodeDown, watch.nodeDownReports = false, 0
	} else {
		watch.nodeDownReports++
		if watch.nodeDownReports == 2 && !watch.nodeDown {
			watch.nodeDown, watch.nodeDownSince = true, time.Now()
			lines := []string{"دیتاسنتر: " + report.DC, "نگهبان روی خود نود در حال تعمیر آن است."}
			if explained := tsin.ExplainFA(tsin.ClassifyText(report.AccountError)); explained != "" {
				lines = append(lines, explained)
			}
			if n := len(report.Log); n > 0 {
				lines = append(lines, "آخرین خط نگهبان: "+report.Log[n-1])
			}
			alert("تونل T.Sin روی نود قطع است؛ کاربرهای کشورها وصل نمی‌شوند", lines...)
		}
	}

	// With the tunnel down every country fails; the tunnel alert covers that.
	failing := ""
	if report.tunnelUp() {
		var status *tsin.Status
		if parsed, err := tsin.ParseStatus(report.Account); err == nil {
			status = &parsed
		}
		failing = strings.Join(report.failingPorts(status), "، ")
	}
	if failing != watch.nodeFailing && time.Since(watch.nodeFailingAt) >= tsinFailingRepeat {
		switch {
		case failing != "":
			alert("این کشورها از طرف T.Sin کار نمی‌کنند", failing,
				"تونل وصل است ولی پراکسی این کشورها در T.Sin جواب نمی‌دهد؛ مشکل از طرف T.Sin است و بقیهٔ کشورها کار می‌کنند.")
		case watch.nodeFailing != "":
			alert("همهٔ کشورهای T.Sin دوباره کار می‌کنند")
		}
		watch.nodeFailing, watch.nodeFailingAt = failing, time.Now()
	}
}
