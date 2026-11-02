package bot

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (b *Bot) handleBackupCommand(ctx context.Context, settings Settings, chatID int64) {
	if b.backup == nil {
		b.reply(ctx, settings, chatID, "بکاپ در دسترس نیست.", nil)
		return
	}
	status, err := b.backup.Status(ctx)
	if err != nil {
		b.reply(ctx, settings, chatID, actionErrorText("خواندن وضعیت بکاپ", err), nil)
		return
	}
	b.reply(ctx, settings, chatID, backupStatusText(status), backupMenuKeyboard())
}

// handleBackupDocument stages an uploaded backup archive for restore. The
// actual restore only runs after the admin taps the confirm button, so a
// document alone never mutates panel state.
func (b *Bot) handleBackupDocument(ctx context.Context, settings Settings, chatID int64, doc *Document) {
	if b.backup == nil {
		b.reply(ctx, settings, chatID, "بکاپ در دسترس نیست.", nil)
		return
	}
	if doc.FileSize > 0 && doc.FileSize > maxTelegramDownloadBytes {
		b.reply(ctx, settings, chatID, fmt.Sprintf(
			"حجم این فایل %s است و از سقف دانلود ربات تلگرام (%d MB) بیشتر است. از صفحه تنظیمات ← بکاپ در پنل بازگردانی‌اش کن.",
			formatBytes(doc.FileSize), maxTelegramDownloadBytes>>20), nil)
		return
	}
	info, err := b.client.getFile(ctx, settings, doc.FileID)
	if err != nil {
		b.reply(ctx, settings, chatID, actionErrorText("گرفتن فایل", err), nil)
		return
	}
	content, err := b.client.downloadFile(ctx, settings, info.FilePath)
	if err != nil {
		b.reply(ctx, settings, chatID, actionErrorText("دانلود فایل", err), nil)
		return
	}
	path, err := stageRestoreUpload(content, doc.FileName)
	if err != nil {
		b.reply(ctx, settings, chatID, actionErrorText("آماده کردن فایل", err), nil)
		return
	}
	if err := b.state.set(ctx, chatID, stateAwaitRestoreConfirm, path); err != nil {
		_ = os.Remove(path)
		b.reply(ctx, settings, chatID, actionErrorText("آماده کردن فایل", err), nil)
		return
	}
	name := strings.TrimSpace(doc.FileName)
	if name == "" {
		name = "فایل بکاپ"
	}
	b.reply(ctx, settings, chatID, fmt.Sprintf(
		"⚠️ <b>بازگردانی %s؟</b>\n\nکل دیتابیس پنل و همه کاربرها با محتوای این فایل جایگزین می‌شوند. "+
			"اول خودکار یک نسخه امن از وضعیت فعلی گرفته می‌شود و اگر بازگردانی خطا بدهد برمی‌گردد، ولی فایل اشتباه می‌تواند تغییرهای اخیر را پاک کند.\n\n"+
			"تا چند دقیقه تأیید کن، یا برای لغو /cancel را بفرست.",
		escape(name)), restoreConfirmKeyboard())
}

func (b *Bot) handleRestoreCallback(ctx context.Context, settings Settings, query *CallbackQuery, chatID, messageID int64, confirmed bool) {
	conv, ok := b.state.get(ctx, chatID)
	if !ok || conv.State != stateAwaitRestoreConfirm {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "چیزی در انتظار نیست")
		return
	}
	_ = b.state.clear(ctx, chatID)
	path := conv.Payload
	defer os.Remove(path)

	if !confirmed {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "لغو شد")
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, "❌ بازگردانی لغو شد. هیچ تغییری داده نشد.", nil)
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "در حال بازگردانی…")
	_ = b.client.editMessageText(ctx, settings, chatID, messageID, "⏳ در حال بازگردانی بکاپ؛ کمی طول می‌کشد…", nil)

	result, err := b.backup.Restore(ctx, path)
	if err != nil {
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, actionErrorText("بازگردانی بکاپ", err), nil)
		return
	}
	_ = b.client.editMessageText(ctx, settings, chatID, messageID, backupRestoreResultText(result), nil)
}

func (b *Bot) handleBackupSetSchedule(ctx context.Context, settings Settings, query *CallbackQuery, chatID, messageID int64, arg string) {
	value, unit, err := parseSchedulePreset(arg)
	if err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "گزینه نامعتبر")
		return
	}
	if err := b.backup.SetSchedule(ctx, value, unit); err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "انجام نشد")
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, actionErrorText("تغییر زمان‌بندی بکاپ", err), nil)
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "زمان‌بندی عوض شد")
	status, err := b.backup.Status(ctx)
	if err != nil {
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, fmt.Sprintf("✅ بکاپ هر %d %s فرستاده می‌شود.", value, backupUnitName(unit)), backupMenuKeyboard())
		return
	}
	_ = b.client.editMessageText(ctx, settings, chatID, messageID, "✅ زمان‌بندی عوض شد.\n\n"+backupStatusText(status), backupMenuKeyboard())
}

func (b *Bot) handleBackupSendNow(ctx context.Context, settings Settings, query *CallbackQuery, chatID, messageID int64) {
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "در حال ارسال…")
	result, err := b.backup.SendNow(ctx)
	if err != nil {
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, actionErrorText("ارسال بکاپ", err), backupMenuKeyboard())
		return
	}
	_ = b.client.editMessageText(ctx, settings, chatID, messageID,
		fmt.Sprintf("✅ بکاپ فرستاده شد: <code>%s</code> (%s).", escape(result.Filename), formatBytes(result.Size)), backupMenuKeyboard())
}

func parseSchedulePreset(arg string) (int, string, error) {
	parts := strings.SplitN(arg, ":", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid preset")
	}
	value, err := strconv.Atoi(parts[0])
	if err != nil || value <= 0 {
		return 0, "", fmt.Errorf("invalid preset")
	}
	unit := parts[1]
	if unit != "hours" && unit != "days" && unit != "minutes" {
		return 0, "", fmt.Errorf("invalid preset")
	}
	return value, unit, nil
}

func stageRestoreUpload(content []byte, filename string) (string, error) {
	suffix := ".rbbackup"
	if trimmed := strings.TrimSpace(filename); trimmed != "" {
		if idx := strings.LastIndex(trimmed, "."); idx >= 0 {
			suffix = trimmed[idx:]
		}
	}
	f, err := os.CreateTemp("", "next-bot-restore-*"+suffix)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func backupStatusText(status BackupStatus) string {
	state := "خاموش"
	if status.Enabled {
		state = fmt.Sprintf("هر %d %s", status.IntervalValue, backupUnitName(status.IntervalUnit))
	}
	lines := []string{
		"📦 <b>بکاپ خودکار</b>",
		line("زمان‌بندی", state),
		line("محتوا", firstNonEmpty(status.Scope, "database")),
		line("آخرین ارسال", formatOptionalTime(status.LastSentAt)),
	}
	if status.LastError != nil && strings.TrimSpace(*status.LastError) != "" {
		lines = append(lines, line("آخرین خطا", *status.LastError))
	}
	lines = append(lines, "", "یک زمان‌بندی انتخاب کن، یا یک فایل بکاپ همین‌جا بفرست تا بازگردانی شود.")
	return strings.Join(lines, "\n")
}

func backupRestoreResultText(result BackupRestoreResult) string {
	lines := []string{
		"✅ <b>بازگردانی انجام شد</b>",
		line("جدول‌ها", strconv.Itoa(result.TablesRestored)),
		line("ردیف‌ها", strconv.Itoa(result.RowsRestored)),
	}
	if len(result.FilesRestored) > 0 {
		lines = append(lines, line("فایل‌ها", strconv.Itoa(len(result.FilesRestored))))
	}
	for _, warning := range result.Warnings {
		lines = append(lines, "⚠️ "+escape(warning))
	}
	if strings.TrimSpace(result.SafetyBackupPath) != "" {
		lines = append(lines, "", "نسخه امن وضعیت قبلی روی سرور اینجا ذخیره شد:", "<code>"+escape(result.SafetyBackupPath)+"</code>")
	}
	return strings.Join(lines, "\n")
}

func formatOptionalTime(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "هنوز نه"
	}
	return *value
}

func backupUnitName(unit string) string {
	switch unit {
	case "hours":
		return "ساعت"
	case "days":
		return "روز"
	case "minutes":
		return "دقیقه"
	}
	return unit
}
