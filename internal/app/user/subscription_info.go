package user

import "strings"

// defaultSubscriptionInfoRemarks are the lines used when an admin turns the
// feature on without writing their own. Each becomes one entry at the top of
// the user's list, so the app itself shows whose subscription it is, what is
// left, and when to refresh it.
var defaultSubscriptionInfoRemarks = []string{
	"✨━━ {USERNAME} ━━✨",
	"{STATUS_EMOJI} حجم باقی‌مانده: {DATA_LEFT} از {DATA_LIMIT}",
	"📶 {DATA_BAR}",
	"📅 {DAYS_LEFT} روز باقی‌مانده",
	"🔄 هر ۱۲ ساعت اشتراک را آپدیت کنید",
}

// subscriptionInfoLinks renders the information entries for an active user.
// They point at 127.0.0.1 port 1, so selecting one connects nowhere; they
// exist only for their names. Users who are expired, limited or disabled get
// the placeholder instead and none of these.
func subscriptionInfoLinks(user UserDetail, settings SubscriptionSettings, format string) []string {
	if !settings.SubscriptionInfoEnabled || format != "v2ray" {
		return nil
	}
	lines := defaultSubscriptionInfoRemarks
	if custom := strings.TrimSpace(settings.SubscriptionInfoRemarks); custom != "" {
		lines = nil
		for _, line := range strings.Split(custom, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	links := make([]string, 0, len(lines))
	for _, line := range lines {
		remark := strings.TrimSpace(formatSubscriptionPlaceholderRemark(user, line))
		if remark != "" {
			links = append(links, subscriptionPlaceholderLink(remark, format))
		}
	}
	return links
}
