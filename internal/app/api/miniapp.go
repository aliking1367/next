package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	telegrambot "github.com/aliking1367/next/internal/app/telegram/bot"
)

// The Telegram Mini App is one page served by the panel and a small API under
// /api/miniapp/. It has no login of its own: every call carries the initData
// Telegram signed with the bot token, and the caller gets exactly what the
// panel's bot gives that Telegram account.

//go:embed miniapp/index.html
var miniAppPage []byte

const (
	miniAppInitDataHeader = "X-Telegram-Init-Data"
	// miniAppMaxAge bounds how long one opening of the Mini App stays valid.
	miniAppMaxAge   = 24 * time.Hour
	miniAppPageSize = 20
)

var (
	errMiniAppUnsigned = errors.New("missing or invalid Telegram signature")
	errMiniAppExpired  = errors.New("this Mini App session is too old; close and open it again")
)

func (s *Server) handleMiniAppPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(miniAppPage)
}

// validateTelegramInitData checks the initData string a Mini App receives
// and returns the Telegram user id it was issued to.
func validateTelegramInitData(initData string, botToken string, now time.Time) (int64, error) {
	values, err := url.ParseQuery(initData)
	if err != nil || strings.TrimSpace(botToken) == "" {
		return 0, errMiniAppUnsigned
	}
	received := values.Get("hash")
	if received == "" {
		return 0, errMiniAppUnsigned
	}
	secret := hmacSHA256([]byte("WebAppData"), []byte(botToken))
	// Telegram hashes every field but the hash. Fields it added later (such
	// as signature) are accepted with or without, so a client of either age
	// passes; both forms are still checked against the bot token.
	if !initDataHashMatches(values, secret, received, nil) && !initDataHashMatches(values, secret, received, map[string]bool{"signature": true}) {
		return 0, errMiniAppUnsigned
	}
	authDate, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
	if err != nil {
		return 0, errMiniAppUnsigned
	}
	issued := time.Unix(authDate, 0)
	if now.Sub(issued) > miniAppMaxAge || issued.After(now.Add(5*time.Minute)) {
		return 0, errMiniAppExpired
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(values.Get("user")), &user); err != nil || user.ID == 0 {
		return 0, errMiniAppUnsigned
	}
	return user.ID, nil
}

func initDataHashMatches(values url.Values, secret []byte, received string, skip map[string]bool) bool {
	keys := make([]string, 0, len(values))
	for key := range values {
		if key == "hash" || skip[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+values.Get(key))
	}
	expected := hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(lines, "\n"))))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(received)))
}

func hmacSHA256(key []byte, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// miniAppCaller is who opened the Mini App: an owner (an id in the bot's
// admin chat list) or an admin through the Telegram id in its profile.
type miniAppCaller struct {
	actor telegrambot.Actor
	owner bool
}

func (s *Server) resolveMiniAppCaller(ctx context.Context, r *http.Request) (miniAppCaller, int, error) {
	settings, err := s.telegramRepo.Settings(ctx)
	if err != nil {
		return miniAppCaller{}, http.StatusServiceUnavailable, errors.New("Telegram settings are unavailable")
	}
	token := ""
	if settings.APIToken != nil {
		token = *settings.APIToken
	}
	if !settings.UseTelegram || strings.TrimSpace(token) == "" {
		return miniAppCaller{}, http.StatusServiceUnavailable, errors.New("the Telegram bot is turned off")
	}
	telegramID, err := validateTelegramInitData(r.Header.Get(miniAppInitDataHeader), token, time.Now())
	if err != nil {
		return miniAppCaller{}, http.StatusUnauthorized, err
	}
	auth := botAuthorizer{server: s}
	for _, id := range settings.AdminChatIDs {
		if id == telegramID {
			actor, ok := auth.Actor(ctx)
			if !ok {
				return miniAppCaller{}, http.StatusForbidden, errors.New("no active full access admin")
			}
			return miniAppCaller{actor: actor, owner: true}, 0, nil
		}
	}
	actor, ok := auth.AdminByTelegramID(ctx, telegramID)
	if !ok {
		return miniAppCaller{}, http.StatusForbidden, errors.New("this Telegram account has no access to the panel")
	}
	actor.Reseller = true
	return miniAppCaller{actor: actor}, 0, nil
}

type miniAppRequest struct {
	Kind      string `json:"kind"`
	Search    string `json:"search"`
	Offset    int64  `json:"offset"`
	Username  string `json:"username"`
	ServiceID int64  `json:"service_id"`
	GB        int64  `json:"gb"`
	Days      int64  `json:"days"`
	Status    string `json:"status"`
	// ResetStrategy is the new user's traffic reset period.
	ResetStrategy string `json:"reset_strategy"`
}

type miniAppUser struct {
	Username     string   `json:"username"`
	Status       string   `json:"status"`
	UsedTraffic  int64    `json:"used_traffic"`
	DataLimit    *int64   `json:"data_limit"`
	Expire       *int64   `json:"expire"`
	OnlineAt     *string  `json:"online_at,omitempty"`
	SubUpdatedAt *string  `json:"sub_updated_at,omitempty"`
	Note         string   `json:"note,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	Reset        string   `json:"reset_strategy,omitempty"`
	Subscription string   `json:"subscription_url,omitempty"`
	Backups      []string `json:"backup_urls,omitempty"`
	Links        []string `json:"links,omitempty"`
}

func miniAppUserFrom(view telegrambot.UserView) miniAppUser {
	return miniAppUser{
		Username:     view.Username,
		Status:       view.Status,
		UsedTraffic:  view.UsedTraffic,
		DataLimit:    view.DataLimit,
		Expire:       view.Expire,
		OnlineAt:     view.OnlineAt,
		SubUpdatedAt: view.SubUpdatedAt,
		Note:         view.Note,
		Owner:        view.OwnerAdmin,
		Reset:        view.ResetStrategy,
		Subscription: view.SubscriptionURL,
		Backups:      view.BackupSubscriptionURLs,
		Links:        view.Links,
	}
}

// handleMiniAppAPI serves POST /api/miniapp/{action}.
func (s *Server) handleMiniAppAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	caller, status, err := s.resolveMiniAppCaller(r.Context(), r)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	var req miniAppRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	users := botUserService{server: s}
	ctx := r.Context()
	actor := caller.actor
	action := strings.TrimPrefix(r.URL.Path, "/api/miniapp/")
	ownerOnly := action == "status" || action == "reset"
	if ownerOnly && !caller.owner {
		writeError(w, http.StatusForbidden, "only the panel owner can do this")
		return
	}

	switch action {
	case "me":
		counts := map[string]int64{}
		for _, kind := range []string{telegrambot.ListAll, telegrambot.ListExpiring, telegrambot.ListFinished} {
			_, total, err := users.List(ctx, actor, telegrambot.UserListQuery{Kind: kind, Limit: 1})
			if err != nil {
				writeError(w, http.StatusBadGateway, err.Error())
				return
			}
			counts[kind] = total
		}
		canDelete := caller.owner
		if admin, ok := actor.Admin.(adminapp.Admin); ok {
			canDelete = admin.Role == adminapp.RoleFullAccess || admin.Permissions.Users.Delete
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"username":   actor.Username,
			"owner":      caller.owner,
			"can_delete": canDelete,
			"can_reset":  actor.CanPeriodicReset,
			"counts":     map[string]int64{"all": counts[telegrambot.ListAll], "expiring": counts[telegrambot.ListExpiring], "finished": counts[telegrambot.ListFinished]},
		})
	case "users":
		kind := req.Kind
		switch kind {
		case telegrambot.ListAll, telegrambot.ListExpiring, telegrambot.ListFinished:
		default:
			kind = telegrambot.ListAll
		}
		search := strings.TrimSpace(req.Search)
		if search != "" {
			kind = telegrambot.ListSearch
		}
		offset := req.Offset
		if offset < 0 {
			offset = 0
		}
		views, total, err := users.List(ctx, actor, telegrambot.UserListQuery{Kind: kind, Search: search, Offset: offset, Limit: miniAppPageSize})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		rows := make([]miniAppUser, 0, len(views))
		for _, view := range views {
			rows = append(rows, miniAppUserFrom(view))
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": rows, "total": total})
	case "user":
		view, err := users.Get(ctx, actor, req.Username)
		if err != nil {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeJSON(w, http.StatusOK, miniAppUserFrom(view))
	case "services":
		services, err := users.Services(ctx, actor)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"services": services})
	case "create":
		if req.GB < 0 || req.Days < 0 {
			writeError(w, http.StatusBadRequest, "traffic and days cannot be negative")
			return
		}
		switch req.ResetStrategy {
		case "", "no_reset", "day", "week", "month", "year":
		default:
			writeError(w, http.StatusBadRequest, "unknown reset period")
			return
		}
		err := users.Create(ctx, actor, telegrambot.NewUser{Username: strings.TrimSpace(req.Username), ServiceID: req.ServiceID, DataLimitGB: req.GB, Days: req.Days, ResetStrategy: req.ResetStrategy})
		s.writeMiniAppUserResult(ctx, w, users, actor, strings.TrimSpace(req.Username), err)
	case "renew":
		if req.GB < 0 || req.Days < 0 || (req.GB == 0 && req.Days == 0) {
			writeError(w, http.StatusBadRequest, "choose days or traffic to add")
			return
		}
		err := users.Renew(ctx, actor, req.Username, req.Days, req.GB)
		s.writeMiniAppUserResult(ctx, w, users, actor, req.Username, err)
	case "delete":
		if err := users.Delete(ctx, actor, req.Username); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": req.Username})
	case "status":
		if req.Status != "active" && req.Status != "disabled" {
			writeError(w, http.StatusBadRequest, "status must be active or disabled")
			return
		}
		err := users.SetStatus(ctx, actor, req.Username, req.Status)
		s.writeMiniAppUserResult(ctx, w, users, actor, req.Username, err)
	case "reset":
		err := users.Reset(ctx, actor, req.Username)
		s.writeMiniAppUserResult(ctx, w, users, actor, req.Username, err)
	default:
		writeError(w, http.StatusNotFound, "unknown action")
	}
}

// writeMiniAppUserResult answers a change with the user as it now is.
func (s *Server) writeMiniAppUserResult(ctx context.Context, w http.ResponseWriter, users botUserService, actor telegrambot.Actor, username string, err error) {
	if err != nil {
		writeError(w, http.StatusBadRequest, botMutationError(err).Error())
		return
	}
	view, err := users.Get(ctx, actor, username)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"username": username})
		return
	}
	writeJSON(w, http.StatusOK, miniAppUserFrom(view))
}
