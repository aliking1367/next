package bot

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
)

var statusNames = map[string]string{
	"active":   "فعال",
	"expired":  "زمانش تمام شده",
	"limited":  "حجمش تمام شده",
	"disabled": "غیرفعال",
	"on_hold":  "منتظر اولین اتصال",
}

var statusEmoji = map[string]string{
	"active":   "✅",
	"expired":  "🕰",
	"limited":  "🪫",
	"disabled": "❌",
	"on_hold":  "🔌",
}

func escape(value string) string {
	return html.EscapeString(value)
}

func line(label, value string) string {
	return fmt.Sprintf("<b>%s:</b> <code>%s</code>", escape(label), escape(value))
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d B", value)
	}
	return fmt.Sprintf("%.2f %s", size, units[unit])
}

func formatOptionalBytes(value *int64) string {
	if value == nil || *value <= 0 {
		return "نامحدود"
	}
	return formatBytes(*value)
}

// formatExpire shows an expiry as a Solar Hijri date in Tehran time, the way
// the admins reading the bot write dates.
func formatExpire(value *int64) string {
	if value == nil || *value <= 0 {
		return "بدون انقضا"
	}
	year, month, day := gregorianToJalali(time.Unix(*value, 0).In(tehran).Date())
	return persianDigits(fmt.Sprintf("%04d/%02d/%02d", year, month, day))
}

// tehran is Iran's offset; Iran has kept +03:30 all year since 2022.
var tehran = time.FixedZone("Asia/Tehran", 3*3600+30*60)

// gregorianToJalali is the same conversion the subscription page uses
// (user.gregorianToJalali), kept here so the bot stays free of that package.
func gregorianToJalali(gy int, gm time.Month, gd int) (int, int, int) {
	gDaysInMonth := []int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	jDaysInMonth := []int{31, 31, 31, 31, 31, 31, 30, 30, 30, 30, 30, 29}
	gy -= 1600
	gmInt := int(gm) - 1
	gd--
	gDayNo := 365*gy + (gy+3)/4 - (gy+99)/100 + (gy+399)/400
	for i := 0; i < gmInt; i++ {
		gDayNo += gDaysInMonth[i]
	}
	if gmInt > 1 && ((gy+1600)%4 == 0 && ((gy+1600)%100 != 0 || (gy+1600)%400 == 0)) {
		gDayNo++
	}
	gDayNo += gd
	jDayNo := gDayNo - 79
	jNP := jDayNo / 12053
	jDayNo %= 12053
	jy := 979 + 33*jNP + 4*(jDayNo/1461)
	jDayNo %= 1461
	if jDayNo >= 366 {
		jy += (jDayNo - 1) / 365
		jDayNo = (jDayNo - 1) % 365
	}
	jm := 0
	for jm < 11 && jDayNo >= jDaysInMonth[jm] {
		jDayNo -= jDaysInMonth[jm]
		jm++
	}
	return jy, jm + 1, jDayNo + 1
}

// daysLeft is the whole days until expiry, or "-" for no expiry.
func daysLeft(value *int64) string {
	if value == nil || *value <= 0 {
		return "-"
	}
	remaining := time.Until(time.Unix(*value, 0))
	if remaining < 0 {
		return persianDigits("0")
	}
	// Rounded up, so a user made for 30 days shows 30, not 29.
	return persianDigits(fmt.Sprintf("%d", int(math.Ceil(remaining.Hours()/24))))
}

// relativeFromServer renders a naive/zoned server timestamp as a coarse relative
// string, mirroring the legacy bot's "about N ... ago" formatting.
func relativeFromServer(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	parsed, ok := parseServerTime(*value)
	if !ok {
		return "-"
	}
	now := time.Now().UTC()
	if parsed.After(now) {
		return coarseDuration(parsed.Sub(now)) + " دیگر"
	}
	return coarseDuration(now.Sub(parsed)) + " پیش"
}

func coarseDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return persianDigits(fmt.Sprintf("حدود %d روز", int(d.Hours()/24)))
	case d >= time.Hour:
		return persianDigits(fmt.Sprintf("حدود %d ساعت", int(d.Hours())))
	case d >= time.Minute:
		return persianDigits(fmt.Sprintf("حدود %d دقیقه", int(d.Minutes())))
	default:
		return "همین الان"
	}
}

func parseServerTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func statusLine(status string) string {
	emoji := statusEmoji[strings.ToLower(strings.TrimSpace(status))]
	if emoji == "" {
		emoji = "•"
	}
	name := statusNames[strings.ToLower(strings.TrimSpace(status))]
	if name == "" {
		name = status
	}
	return fmt.Sprintf("%s <b>%s</b>", emoji, escape(name))
}

// userDetailText renders the admin-facing user detail message.
func userDetailText(user UserView) string {
	dataLeft := "-"
	if user.DataLimit != nil && *user.DataLimit > 0 {
		left := *user.DataLimit - user.UsedTraffic
		if left < 0 {
			left = 0
		}
		dataLeft = formatBytes(left)
	}
	lines := []string{
		"👤 " + statusLine(user.Status),
		separator(),
		line("نام کاربری", user.Username),
		line("حجم کل", formatOptionalBytes(user.DataLimit)),
		line("مصرف شده", formatBytes(user.UsedTraffic)),
		line("باقی‌مانده", dataLeft),
	}
	if reset := strings.TrimSpace(user.ResetStrategy); reset != "" && reset != "no_reset" {
		lines = append(lines, line("ریست حجم", resetLabel(reset)))
	}
	lines = append(lines,
		line("انقضا", formatExpire(user.Expire)),
		line("روزهای باقی‌مانده", daysLeft(user.Expire)),
		line("آخرین اتصال", relativeFromServer(user.OnlineAt)),
		line("آخرین آپدیت ساب", relativeFromServer(user.SubUpdatedAt)),
	)
	if strings.TrimSpace(user.Note) != "" {
		lines = append(lines, line("یادداشت", user.Note))
	}
	if strings.TrimSpace(user.OwnerAdmin) != "" {
		lines = append(lines, line("ادمین", user.OwnerAdmin))
	}
	if strings.TrimSpace(user.SubscriptionURL) != "" {
		lines = append(lines, separator(), line("لینک ساب", user.SubscriptionURL))
		for _, backup := range user.BackupSubscriptionURLs {
			lines = append(lines, line("لینک پشتیبان", backup))
		}
	}
	return strings.Join(lines, "\n")
}

// userUsageText renders the public /usage response.
func userUsageText(user UserView) string {
	return strings.Join([]string{
		"📊 " + statusLine(user.Status),
		separator(),
		line("نام کاربری", user.Username),
		line("حجم کل", formatOptionalBytes(user.DataLimit)),
		line("مصرف شده", formatBytes(user.UsedTraffic)),
		line("انقضا", formatExpire(user.Expire)),
		line("روزهای باقی‌مانده", daysLeft(user.Expire)),
	}, "\n")
}

func systemInfoText(info SystemInfo) string {
	return strings.Join([]string{
		"🖥 <b>وضعیت سرور</b>",
		separator(),
		line("نسخه", firstNonEmpty(info.Version, "-")),
		line("پردازنده", fmt.Sprintf("%.1f%%", info.CPUPercent)),
		line("حافظه", fmt.Sprintf("%s / %s", formatBytes(info.MemUsed), formatBytes(info.MemTotal))),
		separator(),
		line("کل کاربرها", fmt.Sprintf("%d", info.TotalUsers)),
		line("کاربرهای فعال", fmt.Sprintf("%d", info.ActiveUsers)),
		line("آنلاین", fmt.Sprintf("%d", info.OnlineUsers)),
	}, "\n")
}

func separator() string {
	return "━━━━━━━━━━━━"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
