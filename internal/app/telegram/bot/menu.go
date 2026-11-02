package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Labels of the button pad under the typing box. Pressing one sends its text,
// so handleMessage matches these exact strings.
const (
	padCreate      = "➕ ساخت کاربر"
	padSearch      = "🔎 جستجو"
	padUsers       = "👥 کاربرها"
	padMyUsers     = "👥 کاربرهای من"
	padMenu        = "🏠 منو"
	stateSearch    = "search"
	listPageSize   = 10
	maxSearchRunes = 32
)

// Callback data of the main menu and user lists.
const (
	cbMenu       = "menu"
	cbSearch     = "search"
	cbBackupMenu = "backup"
	cbList       = "list:"
	cbShow       = "show:"
)

func usersPadLabel(reseller bool) string {
	if reseller {
		return padMyUsers
	}
	return padUsers
}

// mainPad is the button pad shown under the typing box.
func mainPad(reseller bool) *ReplyKeyboard {
	return &ReplyKeyboard{
		Keyboard: [][]ReplyButton{
			{{Text: padCreate}, {Text: padSearch}},
			{{Text: usersPadLabel(reseller)}, {Text: padMenu}},
		},
		ResizeKeyboard: true,
		IsPersistent:   true,
	}
}

// mainMenuKeyboard is the glass menu under the main menu message.
func mainMenuKeyboard(reseller bool) *InlineKeyboard {
	rows := [][]InlineButton{
		{{Text: padCreate, CallbackData: cbNew}, {Text: padSearch, CallbackData: cbSearch}},
		{{Text: usersPadLabel(reseller), CallbackData: cbList + ListAll + ":0"}, {Text: "⏳ نزدیک به انقضا", CallbackData: cbList + ListExpiring + ":0"}},
	}
	if reseller {
		rows = append(rows, []InlineButton{{Text: "🔴 تمام‌شده‌ها", CallbackData: cbList + ListFinished + ":0"}})
	} else {
		rows = append(rows,
			[]InlineButton{{Text: "🔴 تمام‌شده‌ها", CallbackData: cbList + ListFinished + ":0"}, {Text: "🖥 وضعیت سرور", CallbackData: cbSystem}},
			[]InlineButton{{Text: "📦 بکاپ", CallbackData: cbBackupMenu}},
		)
	}
	return &InlineKeyboard{InlineKeyboard: rows}
}

func menuText(who caller) string {
	if who.reseller() {
		return fmt.Sprintf("🏠 <b>منوی اصلی</b>\nادمین: <b>%s</b>", escape(who.actor.Username))
	}
	return "🏠 <b>منوی اصلی</b>"
}

func (b *Bot) sendStart(ctx context.Context, settings Settings, who caller, chatID int64) {
	b.replyWithPad(ctx, settings, chatID, helpText(who), mainPad(who.reseller()))
	b.reply(ctx, settings, chatID, menuText(who), mainMenuKeyboard(who.reseller()))
}

// handlePadButton runs a press on the button pad and reports whether the
// text was one of its labels. A press drops any half-finished step, the way
// a menu choice would.
func (b *Bot) handlePadButton(ctx context.Context, settings Settings, who caller, chatID int64, text string) bool {
	switch text {
	case padCreate:
		_ = b.state.clear(ctx, chatID)
		b.startCreate(ctx, settings, who, chatID)
	case padSearch:
		b.startSearch(ctx, settings, chatID)
	case padUsers, padMyUsers:
		_ = b.state.clear(ctx, chatID)
		b.sendUserList(ctx, settings, who, chatID, 0, UserListQuery{Kind: ListAll})
	case padMenu:
		_ = b.state.clear(ctx, chatID)
		b.reply(ctx, settings, chatID, menuText(who), mainMenuKeyboard(who.reseller()))
	default:
		return false
	}
	return true
}

func (b *Bot) startSearch(ctx context.Context, settings Settings, chatID int64) {
	_ = b.state.set(ctx, chatID, stateSearch, "")
	b.reply(ctx, settings, chatID, "🔎 نام کاربری یا بخشی از آن را بفرست:", &InlineKeyboard{InlineKeyboard: [][]InlineButton{cancelRow()}})
}

// lookupUser shows the user with exactly this name, or else the users whose
// name contains it.
func (b *Bot) lookupUser(ctx context.Context, settings Settings, who caller, chatID int64, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if user, err := b.users.Get(ctx, who.actor, text); err == nil {
		b.reply(ctx, settings, chatID, userDetailText(user), userMenuKeyboard(user.Username, user.Status, who.reseller()))
		return
	}
	if len([]rune(text)) > maxSearchRunes {
		text = string([]rune(text)[:maxSearchRunes])
	}
	b.sendUserList(ctx, settings, who, chatID, 0, UserListQuery{Kind: ListSearch, Search: text})
}

// sendUserList shows one page of a list, editing messageID when it is set.
func (b *Bot) sendUserList(ctx context.Context, settings Settings, who caller, chatID, messageID int64, query UserListQuery) {
	query.Limit = listPageSize
	if query.Offset < 0 {
		query.Offset = 0
	}
	users, total, err := b.users.List(ctx, who.actor, query)
	var text string
	var keyboard *InlineKeyboard
	if err != nil {
		text = actionErrorText("خواندن لیست", err)
	} else {
		text, keyboard = userListMessage(who, query, users, total)
	}
	if messageID > 0 {
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, text, keyboard)
		return
	}
	b.reply(ctx, settings, chatID, text, keyboard)
}

func listTitle(who caller, query UserListQuery) string {
	switch query.Kind {
	case ListExpiring:
		return "⏳ <b>نزدیک به انقضا</b> (کمتر از ۳ روز یا کمتر از ۱۰٪ حجم)"
	case ListFinished:
		return "🔴 <b>تمام‌شده‌ها</b> (حجم یا زمانشان تمام شده)"
	case ListSearch:
		return fmt.Sprintf("🔎 <b>نتیجه جستجوی</b> <code>%s</code>", escape(query.Search))
	}
	if who.reseller() {
		return "👥 <b>کاربرهای من</b>"
	}
	return "👥 <b>کاربرها</b>"
}

func userListMessage(who caller, query UserListQuery, users []UserView, total int64) (string, *InlineKeyboard) {
	title := listTitle(who, query)
	rows := [][]InlineButton{}
	if total == 0 || len(users) == 0 {
		rows = append(rows, []InlineButton{{Text: padMenu, CallbackData: cbMenu}})
		return title + "\n\nکاربری پیدا نشد.", &InlineKeyboard{InlineKeyboard: rows}
	}
	from := query.Offset + 1
	to := query.Offset + int64(len(users))
	text := fmt.Sprintf("%s\n%s تا %s از %s — روی هر کاربر بزن تا بازش کنی.", title,
		persianDigits(strconv.FormatInt(from, 10)), persianDigits(strconv.FormatInt(to, 10)), persianDigits(strconv.FormatInt(total, 10)))
	for _, user := range users {
		rows = append(rows, []InlineButton{{Text: userButtonText(user), CallbackData: cbShow + user.Username}})
	}
	paging := []InlineButton{}
	if query.Offset > 0 {
		paging = append(paging, InlineButton{Text: "▶️ قبلی", CallbackData: listCallback(query, query.Offset-listPageSize)})
	}
	if to < total {
		paging = append(paging, InlineButton{Text: "بعدی ◀️", CallbackData: listCallback(query, query.Offset+listPageSize)})
	}
	if len(paging) > 0 {
		rows = append(rows, paging)
	}
	rows = append(rows, []InlineButton{{Text: padMenu, CallbackData: cbMenu}})
	return text, &InlineKeyboard{InlineKeyboard: rows}
}

// listCallback encodes a list page. A search keeps its text last so a colon
// in it cannot shift the other fields.
func listCallback(query UserListQuery, offset int64) string {
	if offset < 0 {
		offset = 0
	}
	data := fmt.Sprintf("%s%s:%d", cbList, query.Kind, offset)
	if query.Kind == ListSearch {
		data += ":" + query.Search
	}
	// Telegram caps callback data at 64 bytes.
	for len(data) > 64 {
		runes := []rune(data)
		data = string(runes[:len(runes)-1])
	}
	return data
}

func parseListCallback(value string) (UserListQuery, bool) {
	parts := strings.SplitN(value, ":", 3)
	if len(parts) < 2 {
		return UserListQuery{}, false
	}
	offset, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || offset < 0 {
		return UserListQuery{}, false
	}
	query := UserListQuery{Kind: parts[0], Offset: offset}
	switch query.Kind {
	case ListAll, ListExpiring, ListFinished:
	case ListSearch:
		if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
			return UserListQuery{}, false
		}
		query.Search = parts[2]
	default:
		return UserListQuery{}, false
	}
	return query, true
}

// userButtonText is one list row: status, name, and what is left.
func userButtonText(user UserView) string {
	emoji := statusEmoji[strings.ToLower(strings.TrimSpace(user.Status))]
	if emoji == "" {
		emoji = "•"
	}
	parts := []string{emoji + " " + user.Username}
	if user.Expire != nil && *user.Expire > 0 {
		days := int64(time.Until(time.Unix(*user.Expire, 0)).Hours() / 24)
		if days < 0 {
			days = 0
		}
		parts = append(parts, persianDigits(strconv.FormatInt(days, 10))+" روز")
	} else {
		parts = append(parts, "♾ روز")
	}
	if user.DataLimit != nil && *user.DataLimit > 0 {
		left := *user.DataLimit - user.UsedTraffic
		if left < 0 {
			left = 0
		}
		parts = append(parts, persianDigits(strconv.FormatFloat(float64(left)/float64(1<<30), 'f', 1, 64))+" گیگ")
	} else {
		parts = append(parts, "♾ گیگ")
	}
	return strings.Join(parts, " · ")
}

// handleMenuCallback handles the main menu and list buttons and reports
// whether the callback was one of them.
func (b *Bot) handleMenuCallback(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID, messageID int64, prefix, value string) bool {
	answer := func(text string) { _ = b.client.answerCallbackQuery(ctx, settings, query.ID, text) }
	switch prefix {
	case cbMenu:
		_ = b.state.clear(ctx, chatID)
		answer("")
		_ = b.client.editMessageText(ctx, settings, chatID, messageID, menuText(who), mainMenuKeyboard(who.reseller()))
	case cbSearch:
		answer("")
		b.startSearch(ctx, settings, chatID)
	case cbBackupMenu:
		answer("")
		if query.From == nil || !backupAuthorized(settings, query.From.ID) {
			b.reply(ctx, settings, chatID, backupNotAuthorizedText(), nil)
			return true
		}
		b.handleBackupCommand(ctx, settings, chatID)
	case cbList:
		listQuery, ok := parseListCallback(value)
		if !ok {
			answer("")
			return true
		}
		answer("")
		b.sendUserList(ctx, settings, who, chatID, messageID, listQuery)
	case cbShow:
		answer("")
		b.sendRefreshedUser(ctx, settings, who, chatID, 0, value, "")
	default:
		return false
	}
	return true
}
