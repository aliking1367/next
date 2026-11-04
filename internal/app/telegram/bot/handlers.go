package bot

import (
	"context"
	"fmt"
	"strings"
)

const ownerHelpText = `🤖 <b>ربات مدیریت Next</b>

با دکمه‌های زیر کاربر بساز یا پیدا کن. نام کاربری را هم می‌توانی مستقیم بفرستی.

<b>دستورها</b>
/new — ساخت کاربر جدید
/user <code>نام‌کاربری</code> — اطلاعات و مدیریت کاربر
/usage <code>نام‌کاربری</code> — مصرف کاربر
/system — وضعیت سرور
/backup — زمان‌بندی بکاپ، یا یک فایل بکاپ بفرست تا بازگردانی شود
/cancel — لغو کار نیمه‌تمام
/help — همین راهنما`

const resellerHelpText = `🤖 <b>ربات مدیریت Next</b>

سلام <b>%s</b>! از اینجا کاربرهای خودت را می‌سازی، تمدید و حذف می‌کنی و اطلاعاتشان را می‌بینی. نام کاربری را هم می‌توانی مستقیم بفرستی.

<b>دستورها</b>
/new — ساخت کاربر جدید
/user <code>نام‌کاربری</code> — اطلاعات کاربر
/cancel — لغو کار نیمه‌تمام
/help — همین راهنما`

func helpText(who caller) string {
	if who.reseller() {
		return fmt.Sprintf(resellerHelpText, escape(who.actor.Username))
	}
	return ownerHelpText
}

func isPrivate(chat Chat) bool {
	return chat.Type == "" || chat.Type == "private"
}

func (b *Bot) handleMessage(ctx context.Context, settings Settings, msg *Message) {
	chatID := msg.Chat.ID
	fromID := int64(0)
	if msg.From != nil {
		fromID = msg.From.ID
	}
	who, ok := b.resolveCaller(ctx, settings, chatID, chatID, fromID, isPrivate(msg.Chat))
	if !ok {
		return
	}
	if msg.Document != nil {
		if who.reseller() || !backupAuthorized(settings, chatID) {
			b.reply(ctx, settings, chatID, backupNotAuthorizedText(), nil)
			return
		}
		b.handleBackupDocument(ctx, settings, chatID, msg.Document)
		return
	}
	text := strings.TrimSpace(msg.Text)
	command, arg := splitCommand(text)

	if !strings.HasPrefix(text, "/") {
		if b.handlePadButton(ctx, settings, who, chatID, text) {
			return
		}
		// Multi-step flows take priority over a plain username lookup.
		if conv, ok := b.state.get(ctx, chatID); ok {
			b.handleConversation(ctx, settings, who, chatID, conv, text)
			return
		}
		b.lookupUser(ctx, settings, who, chatID, text)
		return
	}

	switch command {
	case "/start", "/help":
		_ = b.state.clear(ctx, chatID)
		b.sendStart(ctx, settings, who, chatID)
	case "/cancel":
		_ = b.state.clear(ctx, chatID)
		b.reply(ctx, settings, chatID, "لغو شد.", mainMenuKeyboard(who.reseller()))
	case "/new":
		b.startCreate(ctx, settings, who, chatID)
	case "/user":
		b.handleUserCommand(ctx, settings, who, chatID, arg)
	case "/usage":
		b.handleUsageCommand(ctx, settings, who, chatID, arg)
	case "/system":
		if who.reseller() {
			b.reply(ctx, settings, chatID, ownerOnlyText(), nil)
			return
		}
		b.reply(ctx, settings, chatID, b.systemText(ctx), nil)
	case "/backup":
		if who.reseller() || !backupAuthorized(settings, chatID) {
			b.reply(ctx, settings, chatID, backupNotAuthorizedText(), nil)
			return
		}
		b.handleBackupCommand(ctx, settings, chatID)
	default:
		if command != "" {
			b.reply(ctx, settings, chatID, "دستور ناشناخته است. /help را بفرست.", nil)
		}
	}
}

func backupNotAuthorizedText() string {
	return "🔒 بکاپ و بازگردانی فقط از چت مخصوص بکاپ ممکن است. " +
		"آن را در داشبورد، تنظیمات ← تلگرام ← چت بکاپ مشخص کن."
}

func ownerOnlyText() string {
	return "🔒 این کار فقط برای مدیر اصلی پنل است."
}

func (b *Bot) handleUsageCommand(ctx context.Context, settings Settings, who caller, chatID int64, username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		b.reply(ctx, settings, chatID, "استفاده: <code>/usage نام‌کاربری</code>", nil)
		return
	}
	user, err := b.users.Get(ctx, who.actor, username)
	if err != nil {
		b.reply(ctx, settings, chatID, notFoundText(username), nil)
		return
	}
	b.reply(ctx, settings, chatID, userUsageText(user), nil)
}

func (b *Bot) handleUserCommand(ctx context.Context, settings Settings, who caller, chatID int64, username string) {
	username = strings.TrimSpace(username)
	if username == "" {
		b.reply(ctx, settings, chatID, "استفاده: <code>/user نام‌کاربری</code>", nil)
		return
	}
	user, err := b.users.Get(ctx, who.actor, username)
	if err != nil {
		b.reply(ctx, settings, chatID, notFoundText(username), nil)
		return
	}
	b.reply(ctx, settings, chatID, userDetailText(user), userMenuKeyboard(user.Username, user.Status, who.reseller()))
}

func (b *Bot) handleConversation(ctx context.Context, settings Settings, who caller, chatID int64, conv conversation, text string) {
	switch conv.State {
	case stateAwaitNote:
		username := conv.Payload
		_ = b.state.clear(ctx, chatID)
		if who.reseller() {
			return
		}
		if !who.hasActor {
			b.reply(ctx, settings, chatID, noActorText(), nil)
			return
		}
		if err := b.users.SetNote(ctx, who.actor, username, text); err != nil {
			b.reply(ctx, settings, chatID, actionErrorText("ثبت یادداشت", err), nil)
			return
		}
		b.sendRefreshedUser(ctx, settings, who, chatID, 0, username, "یادداشت ثبت شد")
	case stateSearch:
		_ = b.state.clear(ctx, chatID)
		b.lookupUser(ctx, settings, who, chatID, text)
	case stateNewUsername, stateNewService, stateNewGB, stateNewReset, stateNewDays, stateNewConfirm, stateRenewDays, stateRenewGB, stateRenewConfirm:
		b.handleFlowText(ctx, settings, who, chatID, conv, text)
	default:
		_ = b.state.clear(ctx, chatID)
	}
}

// ownerOnlyCallbacks are the buttons only an owner chat may press: they act
// on the whole panel or go beyond create, renew, delete and info.
var ownerOnlyCallbacks = map[string]bool{
	cbActivate:          true,
	cbSuspend:           true,
	cbResetUsage:        true,
	cbRevokeSub:         true,
	cbEditNote:          true,
	cbBackupSetSchedule: true,
	cbBackupSendNow:     true,
	cbRestoreConfirm:    true,
	cbRestoreCancel:     true,
	cbSystem:            true,
	cbBackupMenu:        true,
}

func (b *Bot) handleCallback(ctx context.Context, settings Settings, query *CallbackQuery) {
	userID := int64(0)
	if query.From != nil {
		userID = query.From.ID
	}
	var chatID, messageID int64
	private := true
	if query.Message != nil {
		chatID = query.Message.Chat.ID
		messageID = query.Message.MessageID
		private = isPrivate(query.Message.Chat)
	}
	who, ok := b.resolveCaller(ctx, settings, userID, chatID, userID, private)
	if !ok {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "اجازه دسترسی نداری")
		return
	}

	prefix, value := parseCallback(query.Data)
	if who.reseller() && ownerOnlyCallbacks[prefix] {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "این کار فقط برای مدیر اصلی پنل است")
		return
	}

	switch prefix {
	case cbSystem:
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, b.systemText(ctx), mainMenuKeyboard(false))
		return
	case cbBackupSendNow, cbRestoreConfirm, cbRestoreCancel, cbBackupSetSchedule:
		if !backupAuthorized(settings, userID) {
			_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "بکاپ فقط از چت مخصوص بکاپ")
			return
		}
	}

	if b.handleMenuCallback(ctx, settings, who, query, chatID, messageID, prefix, value) {
		return
	}
	if b.handleFlowCallback(ctx, settings, who, query, chatID, prefix, value) {
		return
	}

	switch prefix {
	case cbBackupSendNow:
		b.handleBackupSendNow(ctx, settings, query, chatID, messageID)
	case cbRestoreConfirm:
		b.handleRestoreCallback(ctx, settings, query, chatID, messageID, true)
	case cbRestoreCancel:
		b.handleRestoreCallback(ctx, settings, query, chatID, messageID, false)
	case cbBackupSetSchedule:
		b.handleBackupSetSchedule(ctx, settings, query, chatID, messageID, value)
	case cbRefresh:
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
		b.sendRefreshedUser(ctx, settings, who, chatID, messageID, value, "")
	case cbLinks:
		b.handleLinks(ctx, settings, who, query, chatID, value)
	case cbEditNote:
		_ = b.state.set(ctx, chatID, stateAwaitNote, value)
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
		b.reply(ctx, settings, chatID, fmt.Sprintf("یادداشت جدید <code>%s</code> را بفرست:", escape(value)), nil)
	case cbDelete:
		if _, err := b.users.Get(ctx, who.actor, value); err != nil {
			_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "کاربر پیدا نشد")
			return
		}
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
		_ = b.client.editMessageText(ctx, settings, chatID, messageID,
			fmt.Sprintf("⚠️ کاربر <code>%s</code> حذف شود؟ این کار برگشت ندارد.", escape(value)),
			confirmDeleteKeyboard(value))
	case cbDeleteYes:
		b.runUserAction(ctx, settings, who, query, chatID, messageID, value, "delete")
	case cbActivate:
		b.runUserAction(ctx, settings, who, query, chatID, messageID, value, "activate")
	case cbSuspend:
		b.runUserAction(ctx, settings, who, query, chatID, messageID, value, "suspend")
	case cbResetUsage:
		b.runUserAction(ctx, settings, who, query, chatID, messageID, value, "reset")
	case cbRevokeSub:
		b.runUserAction(ctx, settings, who, query, chatID, messageID, value, "revoke")
	default:
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
	}
}

func (b *Bot) handleLinks(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID int64, username string) {
	user, err := b.users.Get(ctx, who.actor, username)
	if err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "کاربر پیدا نشد")
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
	b.reply(ctx, settings, chatID, linksText(user), nil)
}

// runUserAction performs a lifecycle action then refreshes the detail message.
func (b *Bot) runUserAction(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID, messageID int64, username, action string) {
	if !who.hasActor {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "هیچ ادمین Full access فعالی نیست")
		return
	}
	var err error
	var note, label string
	switch action {
	case "delete":
		err = b.users.Delete(ctx, who.actor, username)
		note, label = "حذف شد", "حذف کاربر"
	case "activate":
		err = b.users.SetStatus(ctx, who.actor, username, "active")
		note, label = "فعال شد", "فعال کردن"
	case "suspend":
		err = b.users.SetStatus(ctx, who.actor, username, "disabled")
		note, label = "غیرفعال شد", "غیرفعال کردن"
	case "reset":
		err = b.users.Reset(ctx, who.actor, username)
		note, label = "مصرف صفر شد", "ریست مصرف"
	case "revoke":
		err = b.users.RevokeSubscription(ctx, who.actor, username)
		note, label = "لینک ساب عوض شد", "عوض کردن لینک ساب"
	}
	if err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "انجام نشد")
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, actionErrorText(label, err), nil)
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, note)
	if action == "delete" {
		_ = b.client.editMessageText(ctx, settings, chatID, messageID,
			fmt.Sprintf("🗑 کاربر <code>%s</code> حذف شد.", escape(username)), nil)
		return
	}
	b.sendRefreshedUser(ctx, settings, who, chatID, messageID, username, note)
}

// sendRefreshedUser shows a user's detail, editing messageID when it is set
// and sending a new message otherwise.
func (b *Bot) sendRefreshedUser(ctx context.Context, settings Settings, who caller, chatID, messageID int64, username, note string) {
	user, err := b.users.Get(ctx, who.actor, username)
	if err != nil {
		if messageID > 0 {
			_ = b.client.editMessageText(ctx, settings, chatID, messageID, notFoundText(username), nil)
			return
		}
		b.reply(ctx, settings, chatID, notFoundText(username), nil)
		return
	}
	text := userDetailText(user)
	if strings.TrimSpace(note) != "" {
		text = "✅ " + escape(note) + "\n" + text
	}
	keyboard := userMenuKeyboard(user.Username, user.Status, who.reseller())
	if messageID > 0 {
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, text, keyboard)
		return
	}
	b.reply(ctx, settings, chatID, text, keyboard)
}

func (b *Bot) systemText(ctx context.Context) string {
	if b.system == nil {
		return "اطلاعات سرور در دسترس نیست."
	}
	info, err := b.system.Info(ctx)
	if err != nil {
		return "اطلاعات سرور در دسترس نیست."
	}
	return systemInfoText(info)
}

func splitCommand(text string) (command string, arg string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	command = strings.ToLower(parts[0])
	// Strip a @botname suffix from the command.
	if at := strings.Index(command, "@"); at >= 0 {
		command = command[:at]
	}
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}
	return command, arg
}

func notFoundText(username string) string {
	return fmt.Sprintf("کاربر <code>%s</code> پیدا نشد.", escape(username))
}

func noActorText() string {
	return "هیچ ادمین Full access فعالی برای انجام این کار نیست."
}

func actionErrorText(action string, err error) string {
	return fmt.Sprintf("❌ %s انجام نشد: <code>%s</code>", escape(action), escape(err.Error()))
}

func linksText(user UserView) string {
	if len(user.Links) == 0 && strings.TrimSpace(user.SubscriptionURL) == "" {
		return fmt.Sprintf("برای <code>%s</code> لینکی نیست.", escape(user.Username))
	}
	lines := []string{fmt.Sprintf("🔗 <b>لینک‌های</b> <code>%s</code>", escape(user.Username))}
	if strings.TrimSpace(user.SubscriptionURL) != "" {
		lines = append(lines, "", "<b>لینک ساب:</b>", "<code>"+escape(user.SubscriptionURL)+"</code>")
		for _, backup := range user.BackupSubscriptionURLs {
			lines = append(lines, "<code>"+escape(backup)+"</code>")
		}
	}
	if len(user.Links) > 0 {
		lines = append(lines, "")
		for _, link := range user.Links {
			lines = append(lines, "<code>"+escape(link)+"</code>")
		}
	}
	return strings.Join(lines, "\n")
}
