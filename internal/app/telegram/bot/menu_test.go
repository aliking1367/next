package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func callbackDatas(call capturedCall) []string {
	markup, _ := call.body["reply_markup"].(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	var out []string
	for _, row := range rows {
		for _, button := range row.([]any) {
			data, _ := button.(map[string]any)["callback_data"].(string)
			out = append(out, data)
		}
	}
	return out
}

func TestStartSendsButtonPadAndGlassMenu(t *testing.T) {
	b, calls, settings := newTestBot(t, newFakeUsers())
	say(b, settings, 100, 100, "/start")

	var pad, menu capturedCall
	for _, call := range *calls {
		markup, _ := call.body["reply_markup"].(map[string]any)
		if _, ok := markup["keyboard"]; ok {
			pad = call
		}
		if _, ok := markup["inline_keyboard"]; ok {
			menu = call
		}
	}
	if pad.body == nil {
		t.Fatal("/start should set the button pad")
	}
	markup := pad.body["reply_markup"].(map[string]any)
	if markup["is_persistent"] != true || markup["resize_keyboard"] != true {
		t.Fatalf("pad markup = %v", markup)
	}
	if !strings.Contains(fmt.Sprint(markup["keyboard"]), padCreate) {
		t.Fatalf("pad lacks the create button: %v", markup["keyboard"])
	}
	datas := strings.Join(callbackDatas(menu), " ")
	for _, want := range []string{cbNew, cbSearch, cbList + ListAll, cbList + ListExpiring, cbList + ListFinished, cbSystem, cbBackupMenu} {
		if !strings.Contains(datas, want) {
			t.Fatalf("owner menu lacks %q: %s", want, datas)
		}
	}
}

func TestResellerMenuHasNoOwnerButtons(t *testing.T) {
	b, calls, settings := newTestBot(t, newFakeUsers())
	say(b, settings, resellerTelegramID, resellerTelegramID, "/start")
	call, _ := lastCall(*calls, "sendMessage")
	for _, data := range callbackDatas(call) {
		if ownerOnlyCallbacks[data] {
			t.Fatalf("reseller menu offers %q", data)
		}
	}
	press(b, settings, resellerTelegramID, resellerTelegramID, cbBackupMenu)
	press(b, settings, resellerTelegramID, resellerTelegramID, cbSystem)
	for _, call := range *calls {
		if text, _ := call.body["text"].(string); strings.Contains(text, "1.0.0") || strings.Contains(text, "بکاپ خودکار") {
			t.Fatalf("reseller reached an owner screen: %q", text)
		}
	}
}

func TestPadButtonStartsCreateAndDropsAStep(t *testing.T) {
	users := newFakeUsers()
	b, _, settings := newTestBot(t, users)
	say(b, settings, 100, 100, "/new")
	say(b, settings, 100, 100, "ali")
	// A pad press in the middle of a flow starts over instead of being
	// taken as an answer.
	say(b, settings, 100, 100, padCreate)
	if conv, ok := b.state.get(context.Background(), 100); !ok || conv.State != stateNewUsername {
		t.Fatalf("state = %+v", conv)
	}
	if len(users.created) != 0 {
		t.Fatal("nothing should be created")
	}
}

func TestUserListPagesAndOpensAUser(t *testing.T) {
	users := newFakeUsers()
	for i := 0; i < 23; i++ {
		name := fmt.Sprintf("user%02d", i)
		users.users[name] = UserView{Username: name, Status: "active"}
	}
	b, calls, settings := newTestBot(t, users)

	say(b, settings, 100, 100, padUsers)
	call, _ := lastCall(*calls, "sendMessage")
	datas := callbackDatas(call)
	if !strings.Contains(strings.Join(datas, " "), cbShow+"user00") || strings.Contains(strings.Join(datas, " "), cbShow+"user10") {
		t.Fatalf("first page = %v", datas)
	}
	next := ""
	for _, data := range datas {
		if strings.HasPrefix(data, cbList+ListAll+":10") {
			next = data
		}
	}
	if next == "" {
		t.Fatalf("expected a next-page button, got %v", datas)
	}
	press(b, settings, 100, 100, next)
	edit, ok := lastCall(*calls, "editMessageText")
	if !ok || !strings.Contains(strings.Join(callbackDatas(edit), " "), cbShow+"user10") {
		t.Fatalf("second page = %v", callbackDatas(edit))
	}
	press(b, settings, 100, 100, cbShow+"user12")
	shown, _ := lastCall(*calls, "sendMessage")
	if text, _ := shown.body["text"].(string); !strings.Contains(text, "user12") {
		t.Fatalf("expected user12 detail, got %q", text)
	}
}

func TestSearchFallsBackToPartialNames(t *testing.T) {
	users := newFakeUsers()
	users.users["mina_vip"] = UserView{Username: "mina_vip", Status: "active"}
	users.users["mina_2"] = UserView{Username: "mina_2", Status: "limited"}
	users.users["reza"] = UserView{Username: "reza", Status: "active"}
	b, calls, settings := newTestBot(t, users)

	say(b, settings, 100, 100, padSearch)
	say(b, settings, 100, 100, "mina")
	call, _ := lastCall(*calls, "sendMessage")
	datas := strings.Join(callbackDatas(call), " ")
	if !strings.Contains(datas, cbShow+"mina_vip") || !strings.Contains(datas, cbShow+"mina_2") || strings.Contains(datas, "reza") {
		t.Fatalf("search results = %s", datas)
	}
	// An exact name opens the user straight away.
	say(b, settings, 100, 100, "reza")
	call, _ = lastCall(*calls, "sendMessage")
	if text, _ := call.body["text"].(string); !strings.Contains(text, "reza") || !strings.Contains(text, "نام کاربری") {
		t.Fatalf("expected reza's detail, got %q", text)
	}
}

func TestListCallbackRoundTrip(t *testing.T) {
	query := UserListQuery{Kind: ListSearch, Search: "a:b", Offset: 20}
	parsed, ok := parseListCallback(strings.TrimPrefix(listCallback(query, 20), cbList))
	if !ok || parsed.Kind != ListSearch || parsed.Search != "a:b" || parsed.Offset != 20 {
		t.Fatalf("parsed = %+v %v", parsed, ok)
	}
	if _, ok := parseListCallback("x:0"); ok {
		t.Fatal("unknown kind must be refused")
	}
	if len(listCallback(UserListQuery{Kind: ListSearch, Search: strings.Repeat("ب", 40)}, 0)) > 64 {
		t.Fatal("callback data must fit Telegram's 64 bytes")
	}
}
