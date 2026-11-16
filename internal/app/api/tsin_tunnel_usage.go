package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aliking1367/next/internal/app/nodecontroller"
	"github.com/aliking1367/next/internal/app/xrayconfig"
)

// The tunnel usage card shows how much traffic each Iranian tunnel carried,
// tunnel by tunnel (one per tunnel port, named by the address users reach it
// on) and all together: by hour, day, week or month, and the hours of the day
// it is busiest. The node reports each tunnel port's gate inbound's traffic;
// nodecontroller keeps it per half hour in tunnel_usages. Times are Iran's
// (UTC+3:30, no daylight saving since 2022).

var tunnelUsageZone = time.FixedZone("Asia/Tehran", 3*3600+1800)

// tunnelUsageStatsWindow is how far back the totals and busiest hours look.
const tunnelUsageStatsWindow = 30 * 24 * time.Hour

type tunnelUsageTotals struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

func (t *tunnelUsageTotals) add(up, down int64) { t.Up += up; t.Down += down }

type tunnelUsagePoint struct {
	Start time.Time `json:"start"`
	Label string    `json:"label"`
	tunnelUsageTotals
}

type tunnelUsagePeakHour struct {
	// Hour is the hour of the day in Iran's time, 0-23.
	Hour int `json:"hour"`
	// AvgBytes is what that hour carried on an average day; AvgMbps the same
	// as a steady speed over the hour.
	AvgBytes int64   `json:"avg_bytes"`
	AvgMbps  float64 `json:"avg_mbps"`
}

type tunnelUsageBusiest struct {
	Start time.Time `json:"start"`
	Label string    `json:"label"`
	Bytes int64     `json:"bytes"`
	Mbps  float64   `json:"mbps"`
}

type tunnelUsageSummary struct {
	Today   tunnelUsageTotals `json:"today"`
	Last24h tunnelUsageTotals `json:"last_24h"`
	Last7d  tunnelUsageTotals `json:"last_7d"`
	Last30d tunnelUsageTotals `json:"last_30d"`
	// Total is everything since the inbound's counters were last reset; it
	// counts traffic from before the history was kept too.
	Total tunnelUsageTotals `json:"total"`
	// HourlyProfile is the average traffic of each hour of the day over the
	// last 30 days (or since the history starts), and PeakHours the busiest
	// three of them.
	HourlyProfile []int64               `json:"hourly_profile"`
	PeakHours     []tunnelUsagePeakHour `json:"peak_hours"`
	PeakAvgMbps   float64               `json:"peak_avg_mbps"`
	DailyAvgBytes int64                 `json:"daily_avg_bytes"`
	Busiest       *tunnelUsageBusiest   `json:"busiest,omitempty"`
	ProfileDays   int                   `json:"profile_days"`
	Series        []tunnelUsagePoint    `json:"series"`
}

type tunnelUsageItem struct {
	Tag       string   `json:"tag"`
	Label     string   `json:"label"`
	Address   string   `json:"address"`
	Port      int      `json:"port"`
	NodePort  int      `json:"node_port"`
	Countries []string `json:"countries"`
	Emergency bool     `json:"emergency"`
	tunnelUsageSummary
}

type tunnelUsageRow struct {
	bucket   time.Time
	tag      string
	up, down int64
}

func (s *Server) handleCoreTSinTunnelUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := requireServiceSudo(r); err != nil {
		writeServiceError(w, err)
		return
	}
	query := r.URL.Query()
	granularity := normalizeTunnelUsageGranularity(query.Get("granularity"))
	jalali := strings.ToLower(strings.TrimSpace(query.Get("calendar"))) != "gregorian"
	count, _ := strconv.Atoi(query.Get("count"))
	count = clampTunnelUsageCount(granularity, count)

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	now := time.Now()
	starts := tunnelUsageSeriesStarts(granularity, count, now, jalali)
	from := starts[0]
	if statsFrom := now.Add(-tunnelUsageStatsWindow); statsFrom.Before(from) {
		from = statsFrom
	}
	rows, err := s.loadTunnelUsageRows(ctx, from)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := s.tunnelUsageItems(ctx, rows)
	totals := s.loadTunnelInboundTotals(ctx)

	byTag := map[string][]tunnelUsageRow{}
	for _, row := range rows {
		byTag[row.tag] = append(byTag[row.tag], row)
	}
	var all tunnelUsageTotals
	for i := range items {
		items[i].tunnelUsageSummary = summarizeTunnelUsage(byTag[items[i].Tag], granularity, starts, now, jalali)
		items[i].Total = totals[items[i].Tag]
		all.add(items[i].Total.Up, items[i].Total.Down)
	}
	combined := summarizeTunnelUsage(rows, granularity, starts, now, jalali)
	combined.Total = all
	writeJSON(w, http.StatusOK, map[string]any{
		"granularity":  granularity,
		"calendar":     map[bool]string{true: "jalali", false: "gregorian"}[jalali],
		"timezone":     "Asia/Tehran",
		"generated_at": now.UTC(),
		"tunnels":      items,
		"all":          combined,
	})
}

func normalizeTunnelUsageGranularity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "day", "daily":
		return "day"
	case "week", "weekly":
		return "week"
	case "month", "monthly":
		return "month"
	default:
		return "hour"
	}
}

// clampTunnelUsageCount is how many hours, days, weeks or months the chart
// shows: the default, or what was asked within a sane bound.
func clampTunnelUsageCount(granularity string, count int) int {
	defaults := map[string][2]int{"hour": {24, 24 * 14}, "day": {30, 180}, "week": {12, 104}, "month": {12, 36}}
	bound := defaults[granularity]
	if count <= 0 {
		return bound[0]
	}
	if count > bound[1] {
		return bound[1]
	}
	return count
}

func (s *Server) loadTunnelUsageRows(ctx context.Context, from time.Time) ([]tunnelUsageRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bucket, tag, uplink, downlink FROM tunnel_usages WHERE bucket >= ? ORDER BY bucket`,
		from.UTC().Truncate(nodecontroller.TunnelUsageBucket).Unix())
	if err != nil {
		if isMissingTableError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read tunnel usage: %w", err)
	}
	defer rows.Close()
	var out []tunnelUsageRow
	for rows.Next() {
		var row tunnelUsageRow
		var bucket int64
		if err := rows.Scan(&bucket, &row.tag, &row.up, &row.down); err != nil {
			return nil, err
		}
		row.bucket = time.Unix(bucket, 0)
		out = append(out, row)
	}
	return out, rows.Err()
}

func isMissingTableError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such table") || strings.Contains(text, "doesn't exist")
}

// loadTunnelInboundTotals reads the tunnel inbounds' running counters.
func (s *Server) loadTunnelInboundTotals(ctx context.Context) map[string]tunnelUsageTotals {
	out := map[string]tunnelUsageTotals{}
	rows, err := s.db.QueryContext(ctx, `SELECT tag, COALESCE(uplink, 0), COALESCE(downlink, 0) FROM inbounds WHERE tag LIKE 'tsin-tgate-%' OR tag IN (?, ?)`,
		xrayconfig.TSinEmergencyVMessTag, xrayconfig.TSinEmergencySSTag)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var tag string
		var totals tunnelUsageTotals
		if rows.Scan(&tag, &totals.Up, &totals.Down) == nil {
			out[tag] = totals
		}
	}
	return out
}

// tunnelUsageItems lists the tunnels: each tunnel port in the T.Sin settings,
// the emergency tunnel once it is set up, and any tunnel since removed that
// still has history in the window.
func (s *Server) tunnelUsageItems(ctx context.Context, rows []tunnelUsageRow) []tunnelUsageItem {
	settings, _ := s.loadTSinSettings(ctx)
	countriesByPort := map[int][]string{}
	for name, port := range s.loadTSinTunnelCountries(ctx) {
		countriesByPort[port] = append(countriesByPort[port], name)
	}
	seen := map[string]bool{}
	var items []tunnelUsageItem
	for _, port := range s.loadTSinTunnelPorts(ctx) {
		if port.NodePort <= 0 {
			continue
		}
		address := strings.TrimSpace(port.Address)
		if address == "" {
			address = settings.EmergencyAddress
		}
		outer := port.Port
		if outer == 0 {
			outer = port.NodePort
		}
		countries := countriesByPort[port.NodePort]
		sort.Strings(countries)
		tag := xrayconfig.TSinTunnelGatePrefix + strconv.Itoa(port.NodePort)
		seen[tag] = true
		items = append(items, tunnelUsageItem{
			Tag: tag, Label: fmt.Sprintf("%s:%d", address, outer), Address: address, Port: outer,
			NodePort: port.NodePort, Countries: countries,
		})
	}
	hasHistory := map[string]bool{}
	for _, row := range rows {
		hasHistory[row.tag] = true
	}
	if settings.EmergencyAddress != "" || hasHistory[xrayconfig.TSinEmergencyVMessTag] || hasHistory[xrayconfig.TSinEmergencySSTag] {
		for _, item := range []struct {
			tag, name string
			port      int
		}{
			{xrayconfig.TSinEmergencyVMessTag, "VMess", settings.EmergencyVMessPort},
			{xrayconfig.TSinEmergencySSTag, "Shadowsocks", settings.EmergencySSPort},
		} {
			seen[item.tag] = true
			label := "🆘 " + item.name
			if settings.EmergencyAddress != "" {
				label = fmt.Sprintf("🆘 %s %s", settings.EmergencyAddress, item.name)
				if item.port > 0 {
					label = fmt.Sprintf("🆘 %s:%d %s", settings.EmergencyAddress, item.port, item.name)
				}
			}
			items = append(items, tunnelUsageItem{Tag: item.tag, Label: label, Address: settings.EmergencyAddress, Port: item.port, Emergency: true})
		}
	}
	var gone []string
	for tag := range hasHistory {
		if !seen[tag] {
			gone = append(gone, tag)
		}
	}
	sort.Strings(gone)
	for _, tag := range gone {
		port, _ := strconv.Atoi(strings.TrimPrefix(tag, xrayconfig.TSinTunnelGatePrefix))
		items = append(items, tunnelUsageItem{Tag: tag, Label: fmt.Sprintf("%s (removed)", tag), NodePort: port})
	}
	for i := range items {
		if items[i].Countries == nil {
			items[i].Countries = []string{}
		}
	}
	return items
}

// summarizeTunnelUsage adds rows up into the chart's series, the recent
// totals and the busiest hours.
func summarizeTunnelUsage(rows []tunnelUsageRow, granularity string, starts []time.Time, now time.Time, jalali bool) tunnelUsageSummary {
	summary := tunnelUsageSummary{HourlyProfile: make([]int64, 24), PeakHours: []tunnelUsagePeakHour{}}
	series := make([]tunnelUsagePoint, len(starts))
	index := make(map[int64]int, len(starts))
	for i, start := range starts {
		series[i] = tunnelUsagePoint{Start: start.UTC(), Label: tunnelUsageLabel(granularity, start, jalali)}
		index[start.Unix()] = i
	}

	local := now.In(tunnelUsageZone)
	todayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tunnelUsageZone)
	statsFrom := now.Add(-tunnelUsageStatsWindow)
	hourly := map[int64]int64{}
	var first time.Time
	for _, row := range rows {
		at := row.bucket
		if !at.Before(todayStart) {
			summary.Today.add(row.up, row.down)
		}
		if at.After(now.Add(-24 * time.Hour)) {
			summary.Last24h.add(row.up, row.down)
		}
		if at.After(now.Add(-7 * 24 * time.Hour)) {
			summary.Last7d.add(row.up, row.down)
		}
		if !at.Before(statsFrom) {
			summary.Last30d.add(row.up, row.down)
			hour := tunnelUsageBucketStart("hour", at, false)
			hourly[hour.Unix()] += row.up + row.down
			if first.IsZero() || at.Before(first) {
				first = at
			}
		}
		if i, ok := index[tunnelUsageBucketStart(granularity, at, jalali).Unix()]; ok {
			series[i].add(row.up, row.down)
		}
	}
	summary.Series = series

	if first.IsZero() {
		return summary
	}
	// The profile averages each hour of the day over the days the history
	// covers, counting today as a whole day only once it is past that hour.
	days := make([]int, 24)
	cursor := tunnelUsageBucketStart("hour", first, false)
	lastHour := tunnelUsageBucketStart("hour", now, false)
	for !cursor.After(lastHour) {
		days[cursor.In(tunnelUsageZone).Hour()]++
		cursor = cursor.Add(time.Hour)
	}
	var busiest tunnelUsageBusiest
	sums := make([]int64, 24)
	for start, bytes := range hourly {
		at := time.Unix(start, 0)
		sums[at.In(tunnelUsageZone).Hour()] += bytes
		if bytes > busiest.Bytes {
			busiest = tunnelUsageBusiest{Start: at.UTC(), Label: tunnelUsageLabel("hour", at, jalali), Bytes: bytes, Mbps: bytesToMbps(bytes, time.Hour)}
		}
	}
	maxDays := 0
	for hour := range sums {
		if days[hour] > 0 {
			summary.HourlyProfile[hour] = sums[hour] / int64(days[hour])
		}
		if days[hour] > maxDays {
			maxDays = days[hour]
		}
	}
	summary.ProfileDays = maxDays
	var dayTotal int64
	for _, value := range summary.HourlyProfile {
		dayTotal += value
	}
	summary.DailyAvgBytes = dayTotal
	order := make([]int, 24)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return summary.HourlyProfile[order[a]] > summary.HourlyProfile[order[b]] })
	var peakSum float64
	for _, hour := range order[:3] {
		if summary.HourlyProfile[hour] <= 0 {
			break
		}
		mbps := bytesToMbps(summary.HourlyProfile[hour], time.Hour)
		summary.PeakHours = append(summary.PeakHours, tunnelUsagePeakHour{Hour: hour, AvgBytes: summary.HourlyProfile[hour], AvgMbps: mbps})
		peakSum += mbps
	}
	if len(summary.PeakHours) > 0 {
		summary.PeakAvgMbps = peakSum / float64(len(summary.PeakHours))
	}
	if busiest.Bytes > 0 {
		summary.Busiest = &busiest
	}
	return summary
}

func bytesToMbps(bytes int64, span time.Duration) float64 {
	return float64(bytes) * 8 / span.Seconds() / 1e6
}

// tunnelUsageSeriesStarts lists the starts of the chart's last count hours,
// days, weeks (from Saturday) or months, oldest first, the current one last.
func tunnelUsageSeriesStarts(granularity string, count int, now time.Time, jalali bool) []time.Time {
	starts := make([]time.Time, count)
	cursor := tunnelUsageBucketStart(granularity, now, jalali)
	for i := count - 1; i >= 0; i-- {
		starts[i] = cursor
		cursor = tunnelUsageBucketStart(granularity, cursor.Add(-time.Minute), jalali)
	}
	return starts
}

// tunnelUsageBucketStart is the start, in Iran's time, of the hour, day,
// week or month at falls in.
func tunnelUsageBucketStart(granularity string, at time.Time, jalali bool) time.Time {
	local := at.In(tunnelUsageZone)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tunnelUsageZone)
	switch granularity {
	case "day":
		return day
	case "week":
		// Iran's week starts on Saturday.
		return day.AddDate(0, 0, -((int(day.Weekday()) + 1) % 7))
	case "month":
		if jalali {
			_, _, jd := gregorianToJalali(day.Year(), int(day.Month()), day.Day())
			return day.AddDate(0, 0, -(jd - 1))
		}
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, tunnelUsageZone)
	default:
		return time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, tunnelUsageZone)
	}
}

func tunnelUsageLabel(granularity string, start time.Time, jalali bool) string {
	local := start.In(tunnelUsageZone)
	y, m, d := local.Year(), int(local.Month()), local.Day()
	if jalali {
		y, m, d = gregorianToJalali(y, m, d)
	}
	switch granularity {
	case "month":
		return fmt.Sprintf("%04d/%02d", y, m)
	case "day", "week":
		return fmt.Sprintf("%04d/%02d/%02d", y, m, d)
	default:
		return fmt.Sprintf("%02d/%02d %02d:00", m, d, local.Hour())
	}
}

// gregorianToJalali converts a Gregorian date to the Iranian (Jalali) one.
func gregorianToJalali(gy, gm, gd int) (int, int, int) {
	monthDays := [12]int{0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334}
	gy2 := gy
	if gm > 2 {
		gy2 = gy + 1
	}
	days := 355666 + 365*gy + (gy2+3)/4 - (gy2+99)/100 + (gy2+399)/400 + gd + monthDays[gm-1]
	jy := -1595 + 33*(days/12053)
	days %= 12053
	jy += 4 * (days / 1461)
	days %= 1461
	if days > 365 {
		jy += (days - 1) / 365
		days = (days - 1) % 365
	}
	if days < 186 {
		return jy, 1 + days/31, 1 + days%31
	}
	return jy, 7 + (days-186)/30, 1 + (days-186)%30
}
