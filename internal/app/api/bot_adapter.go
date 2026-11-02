package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	adminapp "github.com/aliking1367/next/internal/app/admin"
	dashboardapp "github.com/aliking1367/next/internal/app/dashboard"
	telegramapp "github.com/aliking1367/next/internal/app/telegram"
	telegrambot "github.com/aliking1367/next/internal/app/telegram/bot"
	userapp "github.com/aliking1367/next/internal/app/user"
	webhookapp "github.com/aliking1367/next/internal/app/webhook"
)

// botSettingsSource adapts the Telegram repository to the bot's SettingsSource.
type botSettingsSource struct {
	server *Server
}

func (b botSettingsSource) BotSettings(ctx context.Context) (telegrambot.Settings, error) {
	settings, err := b.server.telegramRepo.Settings(ctx)
	if err != nil {
		return telegrambot.Settings{}, err
	}
	token := ""
	if settings.APIToken != nil {
		token = strings.TrimSpace(*settings.APIToken)
	}
	proxy := ""
	if settings.ProxyURL != nil {
		proxy = strings.TrimSpace(*settings.ProxyURL)
	}
	return telegrambot.Settings{
		Enabled:      settings.UseTelegram,
		Token:        token,
		ProxyURL:     proxy,
		AdminChatIDs: settings.AdminChatIDs,
		BackupChatID: settings.BackupChatID,
	}, nil
}

// botAuthorizer resolves the active full-access admin bot mutations run as.
type botAuthorizer struct {
	server *Server
}

func (b botAuthorizer) Actor(ctx context.Context) (telegrambot.Actor, bool) {
	admin, ok, err := b.server.adminRepo.FirstFullAccessAdmin(ctx)
	if err != nil || !ok {
		return telegrambot.Actor{}, false
	}
	return telegrambot.Actor{Username: admin.Username, Admin: admin}, true
}

// AdminByTelegramID finds the active admin whose profile carries this
// Telegram user id. Two admins sharing an id is ambiguous, so neither gets in.
func (b botAuthorizer) AdminByTelegramID(ctx context.Context, telegramID int64) (telegrambot.Actor, bool) {
	if telegramID == 0 || b.server.db == nil {
		return telegrambot.Actor{}, false
	}
	rows, err := b.server.db.QueryContext(ctx, `SELECT username FROM admins WHERE telegram_id = ? AND status = ? LIMIT 2`, telegramID, string(adminapp.StatusActive))
	if err != nil {
		return telegrambot.Actor{}, false
	}
	usernames := []string{}
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			rows.Close()
			return telegrambot.Actor{}, false
		}
		usernames = append(usernames, username)
	}
	rows.Close()
	if len(usernames) != 1 {
		return telegrambot.Actor{}, false
	}
	admin, ok, err := b.server.adminRepo.AdminByUsername(ctx, usernames[0])
	if err != nil || !ok || admin.Status != adminapp.StatusActive {
		return telegrambot.Actor{}, false
	}
	return telegrambot.Actor{Username: admin.Username, Admin: admin}, true
}

// botUserService adapts the user service to the bot's UserService, enforcing the
// same Go service permission/limit core as the HTTP API.
type botUserService struct {
	server *Server
}

// Get reads a user the way the actor's panel account would: an owner chat
// sees every user, an admin only its own.
func (b botUserService) Get(ctx context.Context, actor telegrambot.Actor, username string) (telegrambot.UserView, error) {
	detail, err := b.detail(ctx, actor, username)
	if err != nil {
		return telegrambot.UserView{}, err
	}
	view := telegrambot.UserView{
		Username:        detail.Username,
		Status:          detail.Status,
		UsedTraffic:     detail.UsedTraffic,
		DataLimit:       detail.DataLimit,
		Expire:          detail.Expire,
		OnlineAt:        detail.OnlineAt,
		SubUpdatedAt:    detail.SubUpdatedAt,
		SubscriptionURL: detail.SubscriptionURL,
		Links:           detail.Links,
	}
	view.BackupSubscriptionURLs = backupSubscriptionURLs(detail)
	if detail.Note != nil {
		view.Note = *detail.Note
	}
	if detail.AdminUsername != nil {
		view.OwnerAdmin = *detail.AdminUsername
	}
	return view, nil
}

func (b botUserService) detail(ctx context.Context, actor telegrambot.Actor, username string) (userapp.UserDetail, error) {
	adminCtx := userapp.AdminContext{
		Username:       "telegram-bot",
		Role:           string(adminapp.RoleFullAccess),
		CanViewTraffic: true,
		CanSortTraffic: true,
	}
	if actor.Reseller {
		admin, err := b.actorAdmin(actor)
		if err != nil {
			return userapp.UserDetail{}, err
		}
		adminCtx = userAdminContextFor(admin, nil)
	}
	return b.server.userService.UserGet(ctx, userapp.UserGetRequest{Username: username, Admin: adminCtx})
}

// backupSubscriptionURLs lists the primary link's own subscription type on
// each backup domain. Taking only the matching type keeps the message to one
// link per domain instead of every link format of every domain.
func backupSubscriptionURLs(detail userapp.UserDetail) []string {
	primaryType := ""
	for _, key := range detail.SubscriptionURLs.Keys() {
		if strings.Contains(key, "@") {
			continue
		}
		if value, ok := detail.SubscriptionURLs.Get(key); ok && value == detail.SubscriptionURL {
			primaryType = key
			break
		}
	}
	if primaryType == "" {
		return nil
	}
	var backups []string
	for _, key := range detail.SubscriptionURLs.Keys() {
		if !strings.HasPrefix(key, primaryType+"@") {
			continue
		}
		if value, ok := detail.SubscriptionURLs.Get(key); ok && strings.TrimSpace(value) != "" {
			backups = append(backups, value)
		}
	}
	return backups
}

// Services lists the services the actor may put a new user on.
func (b botUserService) Services(ctx context.Context, actor telegrambot.Actor) ([]telegrambot.ServiceOption, error) {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return nil, err
	}
	rows, err := b.server.db.QueryContext(ctx, `SELECT id, name FROM services ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assigned := map[int64]bool{}
	for _, id := range admin.Services {
		assigned[id] = true
	}
	options := []telegrambot.ServiceOption{}
	for rows.Next() {
		var option telegrambot.ServiceOption
		if err := rows.Scan(&option.ID, &option.Name); err != nil {
			return nil, err
		}
		if admin.Role.IsGlobal() || assigned[option.ID] {
			options = append(options, option)
		}
	}
	return options, rows.Err()
}

// Create makes a user through the same service and reports as the panel's
// own create endpoint, so limits, Telegram and webhooks see no difference.
func (b botUserService) Create(ctx context.Context, actor telegrambot.Actor, user telegrambot.NewUser) error {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"username":   user.Username,
		"service_id": user.ServiceID,
		"status":     "active",
		"data_limit": int64(0),
		"expire":     int64(0),
	}
	if user.DataLimitGB > 0 {
		payload["data_limit"] = user.DataLimitGB << 30
	}
	if user.Days > 0 {
		payload["expire"] = time.Now().Add(time.Duration(user.Days) * 24 * time.Hour).Unix()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result, err := b.server.userService.CreateUser(ctx, admin, raw)
	if err != nil {
		return botMutationError(err)
	}
	b.server.kickUserNodeOperationsSoon(result.UserID)
	report := userReportForTelegram(result, admin.Username, admin.Username+" (bot)", raw)
	b.server.telegramReports.UserCreated(ctx, report)
	b.server.enqueueWebhook(ctx, webhookUserEvent(webhookapp.ActionUserCreated, report))
	return nil
}

// Renew adds days on top of the current expiry (from now if it already
// passed) and gigabytes on top of the data limit, so a customer keeps what
// was left. An unlimited side stays unlimited; an on-hold user gets the days
// on its waiting period instead.
func (b botUserService) Renew(ctx context.Context, actor telegrambot.Actor, username string, days int64, gigabytes int64) error {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return err
	}
	detail, err := b.detail(ctx, actor, username)
	if err != nil {
		return err
	}
	fields := renewFields(detail, days, gigabytes, time.Now())
	if len(fields) == 0 {
		return errors.New("this user is already unlimited in what you chose to add")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	result, err := b.server.userService.UpdateUser(ctx, admin, detail.Username, raw)
	if err != nil {
		return botMutationError(err)
	}
	b.server.kickUserNodeOperationsSoon(result.UserID)
	report := userReportForTelegram(result, admin.Username, admin.Username+" (bot)", raw)
	b.server.telegramReports.UserUpdated(ctx, report)
	b.server.enqueueWebhook(ctx, webhookUserEvent(webhookapp.ActionUserUpdated, report))
	return nil
}

// renewFields is the update a renewal sends: days go on the expiry (or on
// an on-hold user's waiting period) and gigabytes on the data limit.
func renewFields(detail userapp.UserDetail, days int64, gigabytes int64, now time.Time) map[string]any {
	fields := map[string]any{}
	if days > 0 {
		add := days * 24 * 3600
		switch {
		case detail.Status == "on_hold" && detail.OnHoldExpireDuration != nil && *detail.OnHoldExpireDuration > 0:
			fields["on_hold_expire_duration"] = *detail.OnHoldExpireDuration + add
		case detail.Expire != nil && *detail.Expire > 0:
			base := *detail.Expire
			if base < now.Unix() {
				base = now.Unix()
			}
			fields["expire"] = base + add
		}
	}
	if gigabytes > 0 && detail.DataLimit != nil && *detail.DataLimit > 0 {
		fields["data_limit"] = *detail.DataLimit + gigabytes<<30
	}
	return fields
}

// botMutationError keeps only the panel's own message, which is what an
// admin needs to read in the chat.
func botMutationError(err error) error {
	var mutationErr userapp.MutationError
	if errors.As(err, &mutationErr) && strings.TrimSpace(mutationErr.Detail) != "" {
		return errors.New(mutationErr.Detail)
	}
	return err
}

func (b botUserService) actorAdmin(actor telegrambot.Actor) (adminapp.Admin, error) {
	admin, ok := actor.Admin.(adminapp.Admin)
	if !ok {
		return adminapp.Admin{}, fmt.Errorf("invalid admin actor")
	}
	return admin, nil
}

func (b botUserService) Delete(ctx context.Context, actor telegrambot.Actor, username string) error {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return err
	}
	result, err := b.server.userService.DeleteUser(ctx, admin, username)
	if err != nil {
		return botMutationError(err)
	}
	b.server.kickUserNodeOperationsSoon(result.UserID)
	report := telegramapp.UserReport{
		Username: result.Username,
		Owner:    admin.Username,
		Actor:    admin.Username + " (bot)",
		Status:   result.Status,
	}
	b.server.telegramReports.UserDeleted(ctx, report)
	b.server.enqueueWebhook(ctx, webhookUserEvent(webhookapp.ActionUserDeleted, report))
	return nil
}

func (b botUserService) Reset(ctx context.Context, actor telegrambot.Actor, username string) error {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return err
	}
	_, err = b.server.userService.ResetUser(ctx, admin, username)
	return err
}

func (b botUserService) RevokeSubscription(ctx context.Context, actor telegrambot.Actor, username string) error {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return err
	}
	_, err = b.server.userService.RevokeUserSubscription(ctx, admin, username)
	return err
}

func (b botUserService) SetStatus(ctx context.Context, actor telegrambot.Actor, username string, status string) error {
	return b.update(ctx, actor, username, map[string]any{"status": status})
}

func (b botUserService) SetNote(ctx context.Context, actor telegrambot.Actor, username string, note string) error {
	return b.update(ctx, actor, username, map[string]any{"note": note})
}

func (b botUserService) update(ctx context.Context, actor telegrambot.Actor, username string, fields map[string]any) error {
	admin, err := b.actorAdmin(actor)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = b.server.userService.UpdateUser(ctx, admin, username, raw)
	return err
}

// botSystemService adapts the system stats service to the bot's SystemService.
type botSystemService struct {
	server *Server
}

func (b botSystemService) Info(ctx context.Context) (telegrambot.SystemInfo, error) {
	stats, err := b.server.systemStatsService().Stats(ctx, dashboardapp.AdminContext{
		Username: "telegram-bot",
		Role:     string(adminapp.RoleFullAccess),
	})
	if err != nil {
		return telegrambot.SystemInfo{}, err
	}
	return telegrambot.SystemInfo{
		Version:     stats.Version,
		CPUPercent:  stats.CPUUsage,
		MemUsed:     stats.Memory.Current,
		MemTotal:    stats.Memory.Total,
		TotalUsers:  stats.TotalUser,
		ActiveUsers: stats.UsersActive,
		OnlineUsers: stats.OnlineUsers,
	}, nil
}

// runTelegramBot starts the long-polling Telegram admin bot. It idles until
// Telegram is enabled in the dashboard.
func (s *Server) runTelegramBot(ctx context.Context) {
	b := telegrambot.New(telegrambot.Options{
		APIBase:    s.cfg.TelegramAPIBase,
		Settings:   botSettingsSource{server: s},
		Authorizer: botAuthorizer{server: s},
		Users:      botUserService{server: s},
		System:     botSystemService{server: s},
		Backup:     botBackupService{server: s},
		DB:         s.db,
	})
	b.Run(ctx)
}
