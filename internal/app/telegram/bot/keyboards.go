package bot

import (
	"fmt"
	"strings"
)

// Callback data prefixes for the user detail inline keyboard. They mirror the
// legacy Python callbacks (delete/suspend/activate/reset_usage/revoke_sub/...).
const (
	cbActivate   = "activate:"
	cbSuspend    = "suspend:"
	cbResetUsage = "reset_usage:"
	cbRevokeSub  = "revoke_sub:"
	cbDelete     = "delete:"
	cbDeleteYes  = "delete_yes:"
	cbLinks      = "links:"
	cbEditNote   = "edit_note:"
	cbRefresh    = "refresh:"
	cbSystem     = "system"

	cbNew          = "new"
	cbCancel       = "cancel"
	cbNewRandom    = "nrand"
	cbNewService   = "nsvc:"
	cbNewGB        = "ngb:"
	cbNewDays      = "nday:"
	cbNewConfirm   = "nok"
	cbRenew        = "renew:"
	cbRenewDays    = "rday:"
	cbRenewGB      = "rgb:"
	cbRenewConfirm = "rok"

	cbBackupSetSchedule = "backup_set:"
	cbBackupSendNow     = "backup_send"
	cbRestoreConfirm    = "restore_confirm"
	cbRestoreCancel     = "restore_cancel"
)

// backupSchedulePresets are the interval choices offered on the /backup menu.
// Keeping this to fixed presets (rather than free-text chat input) avoids
// parsing/validating arbitrary admin-typed intervals for a scheduler that
// pushes the entire panel database.
var backupSchedulePresets = []struct {
	Label string
	Value int
	Unit  string
}{
	{"هر ۶ ساعت", 6, "hours"},
	{"هر ۱۲ ساعت", 12, "hours"},
	{"هر ۲۴ ساعت", 24, "hours"},
	{"هر ۳ روز", 3, "days"},
	{"هر ۷ روز", 7, "days"},
}

func backupMenuKeyboard() *InlineKeyboard {
	rows := make([][]InlineButton, 0, len(backupSchedulePresets)+1)
	for _, preset := range backupSchedulePresets {
		rows = append(rows, []InlineButton{{
			Text:         preset.Label,
			CallbackData: fmt.Sprintf("%s%d:%s", cbBackupSetSchedule, preset.Value, preset.Unit),
		}})
	}
	rows = append(rows, []InlineButton{{Text: "📤 ارسال بکاپ همین حالا", CallbackData: cbBackupSendNow, Style: stylePrimary}})
	return &InlineKeyboard{InlineKeyboard: rows}
}

func restoreConfirmKeyboard() *InlineKeyboard {
	return &InlineKeyboard{InlineKeyboard: [][]InlineButton{
		{{Text: "✅ بازگردانی کن", CallbackData: cbRestoreConfirm, Style: styleDanger}, {Text: "✖️ لغو", CallbackData: cbRestoreCancel}},
	}}
}

// userMenuKeyboard builds the keyboard under a user's detail. An admin using
// the bot through its own Telegram id gets links, renew, refresh and delete;
// the owner also gets the status, reset, revoke and note buttons.
func userMenuKeyboard(username string, status string, reseller bool) *InlineKeyboard {
	links := InlineButton{Text: "🔗 لینک‌ها", CallbackData: cbLinks + username}
	renew := InlineButton{Text: "♻️ تمدید", CallbackData: cbRenew + username, Style: stylePrimary}
	refresh := InlineButton{Text: "🔄 بروزرسانی", CallbackData: cbRefresh + username}
	remove := InlineButton{Text: "🗑 حذف", CallbackData: cbDelete + username, Style: styleDanger}
	if reseller {
		return &InlineKeyboard{InlineKeyboard: [][]InlineButton{
			{links, renew},
			{refresh, remove},
		}}
	}
	toggle := InlineButton{Text: "⛔ غیرفعال کن", CallbackData: cbSuspend + username, Style: styleDanger}
	if strings.EqualFold(strings.TrimSpace(status), "disabled") {
		toggle = InlineButton{Text: "✅ فعال کن", CallbackData: cbActivate + username, Style: styleSuccess}
	}
	return &InlineKeyboard{InlineKeyboard: [][]InlineButton{
		{links, renew},
		{toggle, {Text: "0️⃣ ریست مصرف", CallbackData: cbResetUsage + username}},
		{{Text: "🚫 لینک ساب جدید", CallbackData: cbRevokeSub + username}, {Text: "📝 یادداشت", CallbackData: cbEditNote + username}},
		{refresh, remove},
	}}
}

func confirmDeleteKeyboard(username string) *InlineKeyboard {
	return &InlineKeyboard{InlineKeyboard: [][]InlineButton{
		{{Text: "✅ بله، حذف کن", CallbackData: cbDeleteYes + username, Style: styleDanger}, {Text: "✖️ لغو", CallbackData: cbRefresh + username}},
	}}
}

func cancelRow() []InlineButton {
	return []InlineButton{{Text: "✖️ لغو", CallbackData: cbCancel}}
}

// amountKeyboard offers preset amounts for a step that also accepts a typed
// number. zeroLabel names the 0 choice (unlimited, or no change).
func amountKeyboard(prefix string, presets []int64, unit string, zeroLabel string) *InlineKeyboard {
	rows := [][]InlineButton{}
	row := []InlineButton{}
	for _, value := range presets {
		row = append(row, InlineButton{Text: fmt.Sprintf("%s %s", persianDigits(fmt.Sprint(value)), unit), CallbackData: fmt.Sprintf("%s%d", prefix, value)})
		if len(row) == 3 {
			rows = append(rows, row)
			row = []InlineButton{}
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	rows = append(rows, []InlineButton{{Text: zeroLabel, CallbackData: prefix + "0"}}, cancelRow())
	return &InlineKeyboard{InlineKeyboard: rows}
}

func confirmKeyboard(confirm InlineButton) *InlineKeyboard {
	return &InlineKeyboard{InlineKeyboard: [][]InlineButton{{confirm}, cancelRow()}}
}

// parseCallback splits "prefix:value" callback data into the prefix (with colon)
// and the value.
func parseCallback(data string) (prefix string, value string) {
	idx := strings.Index(data, ":")
	if idx < 0 {
		return data, ""
	}
	return data[:idx+1], data[idx+1:]
}
