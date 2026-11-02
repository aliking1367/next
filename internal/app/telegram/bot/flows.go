package bot

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Conversation states of the create and renew flows. Each step also takes a
// typed answer, so the state says which question the next message answers.
const (
	stateNewUsername   = "new_username"
	stateNewService    = "new_service"
	stateNewGB         = "new_gb"
	stateNewDays       = "new_days"
	stateNewConfirm    = "new_confirm"
	stateRenewDays     = "renew_days"
	stateRenewGB       = "renew_gb"
	stateRenewConfirm  = "renew_confirm"
	maxFlowGigabytes   = 100000
	maxFlowDays        = 3650
	randomUsernameSize = 8
)

var (
	gbPresets   = []int64{10, 20, 30, 50, 100}
	dayPresets  = []int64{30, 60, 90}
	usernameRe  = regexp.MustCompile(`^[A-Za-z0-9_]{3,32}$`)
	digitMapper = strings.NewReplacer(
		"۰", "0", "۱", "1", "۲", "2", "۳", "3", "۴", "4", "۵", "5", "۶", "6", "۷", "7", "۸", "8", "۹", "9",
		"٠", "0", "١", "1", "٢", "2", "٣", "3", "٤", "4", "٥", "5", "٦", "6", "٧", "7", "٨", "8", "٩", "9",
	)
)

// flowDraft is what a create or renew flow has collected so far. It rides in
// the conversation payload as JSON.
type flowDraft struct {
	Username  string `json:"u,omitempty"`
	ServiceID int64  `json:"s,omitempty"`
	GB        int64  `json:"g,omitempty"`
	Days      int64  `json:"d,omitempty"`
}

func (b *Bot) loadDraft(ctx context.Context, chatID int64, states ...string) (flowDraft, string, bool) {
	conv, ok := b.state.get(ctx, chatID)
	if !ok {
		return flowDraft{}, "", false
	}
	matched := false
	for _, state := range states {
		if conv.State == state {
			matched = true
		}
	}
	if !matched {
		return flowDraft{}, "", false
	}
	var draft flowDraft
	if strings.TrimSpace(conv.Payload) != "" {
		if err := json.Unmarshal([]byte(conv.Payload), &draft); err != nil {
			return flowDraft{}, "", false
		}
	}
	return draft, conv.State, true
}

func (b *Bot) saveDraft(ctx context.Context, chatID int64, state string, draft flowDraft) {
	raw, _ := json.Marshal(draft)
	_ = b.state.set(ctx, chatID, state, string(raw))
}

// --- create ------------------------------------------------------------------

func (b *Bot) startCreate(ctx context.Context, settings Settings, who caller, chatID int64) {
	if !who.hasActor {
		b.reply(ctx, settings, chatID, noActorText(), nil)
		return
	}
	b.saveDraft(ctx, chatID, stateNewUsername, flowDraft{})
	b.reply(ctx, settings, chatID,
		"➕ <b>ساخت کاربر جدید</b>\n\nنام کاربری را بفرست (۳ تا ۳۲ حرف انگلیسی، عدد یا _)، یا «🎲 تصادفی» را بزن.",
		&InlineKeyboard{InlineKeyboard: [][]InlineButton{{{Text: "🎲 تصادفی", CallbackData: cbNewRandom}}, cancelRow()}})
}

func (b *Bot) createUsernameChosen(ctx context.Context, settings Settings, who caller, chatID int64, username string) {
	username = strings.TrimSpace(username)
	if !usernameRe.MatchString(username) {
		b.reply(ctx, settings, chatID, "این نام کاربری قبول نیست. فقط حروف انگلیسی، عدد و _ و بین ۳ تا ۳۲ حرف. دوباره بفرست:", &InlineKeyboard{InlineKeyboard: [][]InlineButton{cancelRow()}})
		return
	}
	draft := flowDraft{Username: username}
	services, err := b.users.Services(ctx, who.actor)
	if err != nil {
		_ = b.state.clear(ctx, chatID)
		b.reply(ctx, settings, chatID, actionErrorText("خواندن سرویس‌ها", err), nil)
		return
	}
	switch len(services) {
	case 0:
		_ = b.state.clear(ctx, chatID)
		b.reply(ctx, settings, chatID, "هیچ سرویسی برای ساخت کاربر نداری. از پنل یک سرویس به این ادمین بده.", nil)
	case 1:
		draft.ServiceID = services[0].ID
		b.askCreateGB(ctx, settings, chatID, draft)
	default:
		b.saveDraft(ctx, chatID, stateNewService, draft)
		rows := make([][]InlineButton, 0, len(services)+1)
		for _, service := range services {
			rows = append(rows, []InlineButton{{Text: service.Name, CallbackData: fmt.Sprintf("%s%d", cbNewService, service.ID)}})
		}
		rows = append(rows, cancelRow())
		b.reply(ctx, settings, chatID, "سرویس کاربر را انتخاب کن:", &InlineKeyboard{InlineKeyboard: rows})
	}
}

func (b *Bot) askCreateGB(ctx context.Context, settings Settings, chatID int64, draft flowDraft) {
	b.saveDraft(ctx, chatID, stateNewGB, draft)
	b.reply(ctx, settings, chatID, "حجم کاربر چند گیگ باشد؟ انتخاب کن یا عددش را بفرست:",
		amountKeyboard(cbNewGB, gbPresets, "گیگ", "♾ نامحدود"))
}

func (b *Bot) askCreateDays(ctx context.Context, settings Settings, chatID int64, draft flowDraft) {
	b.saveDraft(ctx, chatID, stateNewDays, draft)
	b.reply(ctx, settings, chatID, "مدت کاربر چند روز باشد؟ انتخاب کن یا عددش را بفرست:",
		amountKeyboard(cbNewDays, dayPresets, "روز", "♾ بدون انقضا"))
}

func (b *Bot) askCreateConfirm(ctx context.Context, settings Settings, chatID int64, draft flowDraft) {
	b.saveDraft(ctx, chatID, stateNewConfirm, draft)
	text := strings.Join([]string{
		"📝 <b>ساخت این کاربر؟</b>",
		separator(),
		line("نام کاربری", draft.Username),
		line("حجم", gigabytesLabel(draft.GB, "نامحدود")),
		line("مدت", daysLabel(draft.Days, "بدون انقضا")),
	}, "\n")
	b.reply(ctx, settings, chatID, text, confirmKeyboard(InlineButton{Text: "✅ بساز", CallbackData: cbNewConfirm}))
}

func (b *Bot) createConfirmed(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID int64) {
	draft, _, ok := b.loadDraft(ctx, chatID, stateNewConfirm)
	if !ok {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, staleStepText)
		return
	}
	_ = b.state.clear(ctx, chatID)
	err := b.users.Create(ctx, who.actor, NewUser{
		Username:    draft.Username,
		ServiceID:   draft.ServiceID,
		DataLimitGB: draft.GB,
		Days:        draft.Days,
	})
	if err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "انجام نشد")
		b.reply(ctx, settings, chatID, actionErrorText("ساخت کاربر", err), nil)
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "ساخته شد")
	b.sendRefreshedUser(ctx, settings, who, chatID, 0, draft.Username, "کاربر ساخته شد")
}

// --- renew -------------------------------------------------------------------

func (b *Bot) startRenew(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID int64, username string) {
	if !who.hasActor {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "هیچ ادمین Full access فعالی نیست")
		return
	}
	user, err := b.users.Get(ctx, who.actor, username)
	if err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "کاربر پیدا نشد")
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "")
	b.saveDraft(ctx, chatID, stateRenewDays, flowDraft{Username: user.Username})
	b.reply(ctx, settings, chatID,
		fmt.Sprintf("♻️ <b>تمدید</b> <code>%s</code>\n\nچند روز اضافه شود؟ روزها به تاریخ انقضای فعلی اضافه می‌شوند (اگر تمام شده بود، از امروز). انتخاب کن یا عددش را بفرست:", escape(user.Username)),
		amountKeyboard(cbRenewDays, dayPresets, "روز", "بدون تغییر زمان"))
}

func (b *Bot) askRenewGB(ctx context.Context, settings Settings, chatID int64, draft flowDraft) {
	b.saveDraft(ctx, chatID, stateRenewGB, draft)
	b.reply(ctx, settings, chatID, "چند گیگ به حجم باقی‌مانده اضافه شود؟ انتخاب کن یا عددش را بفرست:",
		amountKeyboard(cbRenewGB, gbPresets, "گیگ", "بدون تغییر حجم"))
}

func (b *Bot) askRenewConfirm(ctx context.Context, settings Settings, chatID int64, draft flowDraft) {
	if draft.Days == 0 && draft.GB == 0 {
		_ = b.state.clear(ctx, chatID)
		b.reply(ctx, settings, chatID, "نه روزی انتخاب شد و نه حجمی؛ تمدید انجام نشد.", nil)
		return
	}
	b.saveDraft(ctx, chatID, stateRenewConfirm, draft)
	text := strings.Join([]string{
		"📝 <b>تمدید این کاربر؟</b>",
		separator(),
		line("نام کاربری", draft.Username),
		line("روز اضافه", daysLabel(draft.Days, "بدون تغییر")),
		line("حجم اضافه", gigabytesLabel(draft.GB, "بدون تغییر")),
	}, "\n")
	b.reply(ctx, settings, chatID, text, confirmKeyboard(InlineButton{Text: "✅ تمدید کن", CallbackData: cbRenewConfirm}))
}

func (b *Bot) renewConfirmed(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID int64) {
	draft, _, ok := b.loadDraft(ctx, chatID, stateRenewConfirm)
	if !ok {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, staleStepText)
		return
	}
	_ = b.state.clear(ctx, chatID)
	if err := b.users.Renew(ctx, who.actor, draft.Username, draft.Days, draft.GB); err != nil {
		_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "انجام نشد")
		b.reply(ctx, settings, chatID, actionErrorText("تمدید", err), nil)
		return
	}
	_ = b.client.answerCallbackQuery(ctx, settings, query.ID, "تمدید شد")
	b.sendRefreshedUser(ctx, settings, who, chatID, 0, draft.Username, "تمدید شد")
}

// --- routing -----------------------------------------------------------------

const staleStepText = "این مرحله تمام شده؛ دوباره شروع کن"

// handleFlowText takes a typed answer to the current create or renew step.
func (b *Bot) handleFlowText(ctx context.Context, settings Settings, who caller, chatID int64, conv conversation, text string) {
	var draft flowDraft
	if strings.TrimSpace(conv.Payload) != "" {
		_ = json.Unmarshal([]byte(conv.Payload), &draft)
	}
	if conv.State == stateNewUsername {
		b.createUsernameChosen(ctx, settings, who, chatID, text)
		return
	}
	if conv.State == stateNewService || conv.State == stateNewConfirm || conv.State == stateRenewConfirm {
		b.reply(ctx, settings, chatID, "یکی از دکمه‌های بالا را بزن، یا /cancel را بفرست.", nil)
		return
	}
	limit := int64(maxFlowGigabytes)
	if conv.State == stateNewDays || conv.State == stateRenewDays {
		limit = maxFlowDays
	}
	value, ok := parseAmount(text, limit)
	if !ok {
		b.reply(ctx, settings, chatID, fmt.Sprintf("یک عدد بین ۰ و %s بفرست، یا یکی از دکمه‌ها را بزن.", persianDigits(strconv.FormatInt(limit, 10))), &InlineKeyboard{InlineKeyboard: [][]InlineButton{cancelRow()}})
		return
	}
	b.flowAmountChosen(ctx, settings, chatID, conv.State, draft, value)
}

func (b *Bot) flowAmountChosen(ctx context.Context, settings Settings, chatID int64, state string, draft flowDraft, value int64) {
	switch state {
	case stateNewGB:
		draft.GB = value
		b.askCreateDays(ctx, settings, chatID, draft)
	case stateNewDays:
		draft.Days = value
		b.askCreateConfirm(ctx, settings, chatID, draft)
	case stateRenewDays:
		draft.Days = value
		b.askRenewGB(ctx, settings, chatID, draft)
	case stateRenewGB:
		draft.GB = value
		b.askRenewConfirm(ctx, settings, chatID, draft)
	}
}

// handleFlowCallback handles the create and renew buttons and reports whether
// the callback was one of them.
func (b *Bot) handleFlowCallback(ctx context.Context, settings Settings, who caller, query *CallbackQuery, chatID int64, prefix, value string) bool {
	answer := func(text string) { _ = b.client.answerCallbackQuery(ctx, settings, query.ID, text) }
	switch prefix {
	case cbNew:
		answer("")
		b.startCreate(ctx, settings, who, chatID)
	case cbCancel:
		_ = b.state.clear(ctx, chatID)
		answer("لغو شد")
		b.reply(ctx, settings, chatID, "لغو شد.", mainMenuKeyboard(who.reseller()))
	case cbNewRandom:
		if _, _, ok := b.loadDraft(ctx, chatID, stateNewUsername); !ok {
			answer(staleStepText)
			return true
		}
		answer("")
		b.createUsernameChosen(ctx, settings, who, chatID, randomUsername())
	case cbNewService:
		draft, _, ok := b.loadDraft(ctx, chatID, stateNewService)
		id, err := strconv.ParseInt(value, 10, 64)
		if !ok || err != nil {
			answer(staleStepText)
			return true
		}
		services, err := b.users.Services(ctx, who.actor)
		if err != nil || !serviceListed(services, id) {
			answer("این سرویس در دسترس نیست")
			return true
		}
		answer("")
		draft.ServiceID = id
		b.askCreateGB(ctx, settings, chatID, draft)
	case cbNewGB, cbNewDays, cbRenewDays, cbRenewGB:
		state := map[string]string{cbNewGB: stateNewGB, cbNewDays: stateNewDays, cbRenewDays: stateRenewDays, cbRenewGB: stateRenewGB}[prefix]
		draft, _, ok := b.loadDraft(ctx, chatID, state)
		amount, err := strconv.ParseInt(value, 10, 64)
		if !ok || err != nil || amount < 0 {
			answer(staleStepText)
			return true
		}
		answer("")
		b.flowAmountChosen(ctx, settings, chatID, state, draft, amount)
	case cbNewConfirm:
		b.createConfirmed(ctx, settings, who, query, chatID)
	case cbRenew:
		b.startRenew(ctx, settings, who, query, chatID, value)
	case cbRenewConfirm:
		b.renewConfirmed(ctx, settings, who, query, chatID)
	default:
		return false
	}
	return true
}

// --- helpers -----------------------------------------------------------------

func serviceListed(services []ServiceOption, id int64) bool {
	for _, service := range services {
		if service.ID == id {
			return true
		}
	}
	return false
}

// parseAmount reads a whole number typed with Latin, Persian or Arabic digits.
func parseAmount(text string, limit int64) (int64, bool) {
	text = strings.TrimSpace(digitMapper.Replace(text))
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 || value > limit {
		return 0, false
	}
	return value, true
}

func persianDigits(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= '0' && r <= '9' {
			out.WriteRune('۰' + (r - '0'))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func gigabytesLabel(value int64, zero string) string {
	if value <= 0 {
		return zero
	}
	return persianDigits(strconv.FormatInt(value, 10)) + " گیگ"
}

func daysLabel(value int64, zero string) string {
	if value <= 0 {
		return zero
	}
	return persianDigits(strconv.FormatInt(value, 10)) + " روز"
}

func randomUsername() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, randomUsernameSize)
	_, _ = rand.Read(buf)
	for i := range buf {
		buf[i] = alphabet[int(buf[i])%len(alphabet)]
	}
	return "u" + string(buf[1:])
}
