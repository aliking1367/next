package bot

import (
	"context"
	"strings"
	"testing"
)

func press(b *Bot, settings Settings, from, chat int64, data string) {
	b.handleCallback(context.Background(), settings, &CallbackQuery{
		ID:      "cb",
		From:    &User{ID: from},
		Message: &Message{MessageID: 7, Chat: Chat{ID: chat, Type: "private"}},
		Data:    data,
	})
}

func say(b *Bot, settings Settings, from, chat int64, text string) {
	b.handleMessage(context.Background(), settings, &Message{
		From: &User{ID: from},
		Chat: Chat{ID: chat, Type: "private"},
		Text: text,
	})
}

func TestCreateFlowWithButtonsAndTypedNumbers(t *testing.T) {
	users := newFakeUsers()
	users.services = []ServiceOption{{ID: 1, Name: "main"}, {ID: 2, Name: "vip"}}
	b, calls, settings := newTestBot(t, users)

	say(b, settings, 100, 100, "/new")
	say(b, settings, 100, 100, "bad name!")
	if conv, ok := b.state.get(context.Background(), 100); !ok || conv.State != stateNewUsername {
		t.Fatalf("a rejected name should keep asking, state=%+v", conv)
	}
	say(b, settings, 100, 100, "ali_2")
	press(b, settings, 100, 100, cbNewService+"2")
	say(b, settings, 100, 100, "۲۵") // Persian digits
	press(b, settings, 100, 100, cbNewDays+"30")
	if len(users.created) != 0 {
		t.Fatal("nothing may be created before the confirm button")
	}
	press(b, settings, 100, 100, cbNewConfirm)

	if len(users.created) != 1 {
		t.Fatalf("created = %+v", users.created)
	}
	got := users.created[0]
	if got.Username != "ali_2" || got.ServiceID != 2 || got.DataLimitGB != 25 || got.Days != 30 {
		t.Fatalf("created = %+v", got)
	}
	if _, ok := b.state.get(context.Background(), 100); ok {
		t.Fatal("state should be cleared after create")
	}
	call, _ := lastCall(*calls, "sendMessage")
	if text, _ := call.body["text"].(string); !strings.Contains(text, "ali_2") {
		t.Fatalf("expected the new user's detail, got %q", text)
	}
}

func TestCreateFlowSkipsServiceStepWithOneService(t *testing.T) {
	users := newFakeUsers()
	b, _, settings := newTestBot(t, users)

	say(b, settings, 100, 100, "/new")
	say(b, settings, 100, 100, "bob")
	if conv, ok := b.state.get(context.Background(), 100); !ok || conv.State != stateNewGB {
		t.Fatalf("with one service the next step is traffic, state=%+v", conv)
	}
	press(b, settings, 100, 100, cbNewGB+"0")
	press(b, settings, 100, 100, cbNewDays+"0")
	press(b, settings, 100, 100, cbNewConfirm)
	if len(users.created) != 1 || users.created[0].ServiceID != 1 || users.created[0].DataLimitGB != 0 || users.created[0].Days != 0 {
		t.Fatalf("created = %+v", users.created)
	}
}

func TestStaleConfirmDoesNothing(t *testing.T) {
	users := newFakeUsers()
	b, _, settings := newTestBot(t, users)
	press(b, settings, 100, 100, cbNewConfirm)
	press(b, settings, 100, 100, cbRenewConfirm)
	if len(users.created) != 0 || len(users.renewals) != 0 {
		t.Fatalf("a confirm without a flow must not act: %+v %+v", users.created, users.renewals)
	}
}

func TestRenewFlow(t *testing.T) {
	users := newFakeUsers()
	users.users["alice"] = UserView{Username: "alice", Status: "limited"}
	b, _, settings := newTestBot(t, users)

	press(b, settings, 100, 100, cbRenew+"alice")
	press(b, settings, 100, 100, cbRenewDays+"30")
	say(b, settings, 100, 100, "50")
	press(b, settings, 100, 100, cbRenewConfirm)

	if len(users.renewals) != 1 || users.renewals[0] != "alice:30d:50g" {
		t.Fatalf("renewals = %v", users.renewals)
	}
}

func TestRenewWithNothingChosenDoesNothing(t *testing.T) {
	users := newFakeUsers()
	users.users["alice"] = UserView{Username: "alice", Status: "active"}
	b, _, settings := newTestBot(t, users)

	press(b, settings, 100, 100, cbRenew+"alice")
	press(b, settings, 100, 100, cbRenewDays+"0")
	press(b, settings, 100, 100, cbRenewGB+"0")
	press(b, settings, 100, 100, cbRenewConfirm)
	if len(users.renewals) != 0 {
		t.Fatalf("renewals = %v", users.renewals)
	}
}

func TestResellerGetsItsOwnLimitedBot(t *testing.T) {
	users := newFakeUsers()
	users.users["alice"] = UserView{Username: "alice", Status: "active"}
	b, calls, settings := newTestBot(t, users)

	say(b, settings, resellerTelegramID, resellerTelegramID, "/user alice")
	call, ok := lastCall(*calls, "sendMessage")
	if !ok {
		t.Fatal("a reseller in its private chat should get an answer")
	}
	if len(users.getActors) == 0 || !users.getActors[len(users.getActors)-1] {
		t.Fatal("the lookup must run as the reseller, not as the owner")
	}
	markup, _ := call.body["reply_markup"].(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	for _, row := range rows {
		for _, button := range row.([]any) {
			data, _ := button.(map[string]any)["callback_data"].(string)
			for prefix := range ownerOnlyCallbacks {
				if strings.HasPrefix(data, prefix) {
					t.Fatalf("reseller keyboard offers owner-only button %q", data)
				}
			}
		}
	}

	// Owner-only buttons are refused even if pressed by hand.
	press(b, settings, resellerTelegramID, resellerTelegramID, cbSuspend+"alice")
	press(b, settings, resellerTelegramID, resellerTelegramID, cbResetUsage+"alice")
	if len(users.statuses) != 0 || len(users.resets) != 0 {
		t.Fatalf("reseller changed status=%v resets=%v", users.statuses, users.resets)
	}

	// Its own actions work.
	press(b, settings, resellerTelegramID, resellerTelegramID, cbDeleteYes+"alice")
	if len(users.deleted) != 1 {
		t.Fatalf("reseller delete = %v", users.deleted)
	}
}

func TestResellerIgnoredOutsideItsPrivateChat(t *testing.T) {
	users := newFakeUsers()
	users.users["alice"] = UserView{Username: "alice", Status: "active"}
	b, calls, settings := newTestBot(t, users)

	// The admin's id, but in a group chat.
	b.handleMessage(context.Background(), settings, &Message{
		From: &User{ID: resellerTelegramID},
		Chat: Chat{ID: -4001, Type: "supergroup"},
		Text: "/user alice",
	})
	// A stranger in a private chat.
	say(b, settings, 777, 777, "/user alice")
	if len(*calls) != 0 {
		t.Fatalf("expected no replies, got %d", len(*calls))
	}
}

func TestResellerCannotUseSystemOrBackup(t *testing.T) {
	b, calls, settings := newTestBot(t, newFakeUsers())
	say(b, settings, resellerTelegramID, resellerTelegramID, "/system")
	call, _ := lastCall(*calls, "sendMessage")
	if text, _ := call.body["text"].(string); strings.Contains(text, "1.0.0") {
		t.Fatal("reseller must not see system info")
	}
	say(b, settings, resellerTelegramID, resellerTelegramID, "/backup")
	call, _ = lastCall(*calls, "sendMessage")
	if text, _ := call.body["text"].(string); !strings.Contains(text, "چت مخصوص بکاپ") {
		t.Fatalf("expected backup refusal, got %q", text)
	}
}

func TestParseAmountAcceptsPersianDigits(t *testing.T) {
	for input, want := range map[string]int64{"30": 30, "۳۰": 30, "٣٠": 30, " 7 ": 7} {
		if got, ok := parseAmount(input, 100); !ok || got != want {
			t.Errorf("parseAmount(%q) = %d,%v", input, got, ok)
		}
	}
	for _, input := range []string{"-1", "abc", "101", ""} {
		if _, ok := parseAmount(input, 100); ok {
			t.Errorf("parseAmount(%q) should fail", input)
		}
	}
}

func TestFormatExpireIsSolarHijri(t *testing.T) {
	// 2026-03-21 12:00 UTC is 1 Farvardin 1405.
	value := int64(1774094400)
	if got := formatExpire(&value); got != "۱۴۰۵/۰۱/۰۱" {
		t.Fatalf("formatExpire = %q", got)
	}
}

// panicUsers fails the way an unexpected bug would.
type panicUsers struct{ *fakeUsers }

func (panicUsers) Get(context.Context, Actor, string) (UserView, error) {
	panic("unexpected failure")
}

func TestAFailingUpdateDoesNotStopTheBot(t *testing.T) {
	users := newFakeUsers()
	b, calls, settings := newTestBot(t, panicUsers{users})
	// Must not panic out of handleUpdate.
	b.handleUpdate(context.Background(), settings, Update{UpdateID: 1, Message: &Message{From: &User{ID: 100}, Chat: Chat{ID: 100}, Text: "/user alice"}})
	// The next update is served as usual.
	b.handleUpdate(context.Background(), settings, Update{UpdateID: 2, Message: &Message{From: &User{ID: 100}, Chat: Chat{ID: 100}, Text: "/help"}})
	if _, ok := lastCall(*calls, "sendMessage"); !ok {
		t.Fatal("the bot should keep answering after a failed update")
	}
}

func TestCreateFlowAsksForTheResetPeriodWhenAllowed(t *testing.T) {
	users := newFakeUsers()
	b, _, settings := newTestBot(t, users)
	b.authorizer = fakeAuthorizer{ok: true, canReset: true}

	say(b, settings, 100, 100, "/new")
	say(b, settings, 100, 100, "monthly_user")
	press(b, settings, 100, 100, cbNewGB+"50")
	if conv, ok := b.state.get(context.Background(), 100); !ok || conv.State != stateNewReset {
		t.Fatalf("after traffic the reset period should be asked, state=%+v", conv)
	}
	press(b, settings, 100, 100, cbNewReset+"evil")
	if conv, _ := b.state.get(context.Background(), 100); conv.State != stateNewReset {
		t.Fatal("an unknown period must not be taken")
	}
	press(b, settings, 100, 100, cbNewReset+"month")
	press(b, settings, 100, 100, cbNewDays+"30")
	press(b, settings, 100, 100, cbNewConfirm)
	if len(users.created) != 1 || users.created[0].ResetStrategy != "month" {
		t.Fatalf("created = %+v", users.created)
	}
}

func TestCreateFlowSkipsTheResetPeriodForUnlimitedOrNotAllowed(t *testing.T) {
	users := newFakeUsers()
	b, _, settings := newTestBot(t, users)
	b.authorizer = fakeAuthorizer{ok: true, canReset: true}
	say(b, settings, 100, 100, "/new")
	say(b, settings, 100, 100, "free_user")
	press(b, settings, 100, 100, cbNewGB+"0")
	if conv, _ := b.state.get(context.Background(), 100); conv.State != stateNewDays {
		t.Fatalf("an unlimited user goes straight to days, state=%+v", conv)
	}

	b.authorizer = fakeAuthorizer{ok: true}
	say(b, settings, 100, 100, "/cancel")
	say(b, settings, 100, 100, "/new")
	say(b, settings, 100, 100, "plain_user")
	press(b, settings, 100, 100, cbNewGB+"20")
	if conv, _ := b.state.get(context.Background(), 100); conv.State != stateNewDays {
		t.Fatalf("an account without the permission is not asked, state=%+v", conv)
	}
}
