// Package bot implements the Next Telegram admin bot using getUpdates
// long-polling, matching the transport of the legacy Python bot. It is
// decoupled from the concrete Go services through the interfaces below; the API
// layer wires real implementations so bot commands reuse the same Go services as
// the HTTP API instead of touching the database directly.
package bot

import "context"

// Update mirrors the subset of the Telegram getUpdates response the bot uses.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64     `json:"message_id"`
	From      *User     `json:"from"`
	Chat      Chat      `json:"chat"`
	Text      string    `json:"text"`
	Caption   string    `json:"caption"`
	Document  *Document `json:"document"`
}

// Document mirrors the subset of a Telegram document attachment the bot needs
// to stage a restore upload.
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type User struct {
	ID int64 `json:"id"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// InlineKeyboard is a Telegram inline_keyboard markup.
type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

// ReplyKeyboard is the button pad Telegram shows in place of the typing
// keyboard. Pressing a button sends its text as a message.
type ReplyKeyboard struct {
	Keyboard       [][]ReplyButton `json:"keyboard"`
	ResizeKeyboard bool            `json:"resize_keyboard"`
	IsPersistent   bool            `json:"is_persistent"`
}

type ReplyButton struct {
	Text string `json:"text"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// Settings is the snapshot the bot needs each poll cycle.
type Settings struct {
	Enabled      bool
	Token        string
	ProxyURL     string
	AdminChatIDs []int64
	// BackupChatID, when set, is the only chat allowed to run /backup
	// commands (schedule changes, on-demand send, restore). Backup/restore
	// moves or replaces the entire panel database, so it deliberately is not
	// available to the wider AdminChatIDs allowlist used for routine
	// user-management commands.
	BackupChatID *int64
}

// SettingsSource provides the current Telegram settings.
type SettingsSource interface {
	BotSettings(ctx context.Context) (Settings, error)
}

// Actor identifies the admin a bot action runs as. Admin is carried opaquely so
// the bot package does not depend on the admin package; the API adapter casts it
// back to its concrete admin type.
type Actor struct {
	Username string
	Admin    any
	// Reseller marks an admin that reached the bot through the Telegram id
	// in its own profile. It sees only the users its panel account sees and
	// gets the create, renew, delete and info actions; everything else stays
	// with the owner chats in Settings.AdminChatIDs.
	Reseller bool
}

// Authorizer resolves the admin a bot action runs as. Owner chats are the
// allowlist in Settings.AdminChatIDs and act as Actor (typically a
// full-access admin); AdminByTelegramID finds the admin whose profile carries
// the sender's Telegram id.
type Authorizer interface {
	Actor(ctx context.Context) (Actor, bool)
	AdminByTelegramID(ctx context.Context, telegramID int64) (Actor, bool)
}

// UserView is the data the bot renders for a user.
type UserView struct {
	Username        string
	Status          string
	UsedTraffic     int64
	DataLimit       *int64
	Expire          *int64
	OnlineAt        *string
	SubUpdatedAt    *string
	Note            string
	OwnerAdmin      string
	SubscriptionURL string
	// BackupSubscriptionURLs are the same subscription on the panel's backup
	// domains. They are listed beside the main link so an admin hands a user
	// every one at once: a client holding them all survives one domain being
	// blocked without the admin having to send anything again.
	BackupSubscriptionURLs []string
	Links                  []string
}

// ServiceOption is a service a new user can be put on.
type ServiceOption struct {
	ID   int64
	Name string
}

// NewUser is what the create flow collects. Zero DataLimitGB or Days means
// no limit.
type NewUser struct {
	Username    string
	ServiceID   int64
	DataLimitGB int64
	Days        int64
}

// User list kinds the bot offers.
const (
	ListAll      = "a"
	ListExpiring = "e"
	ListFinished = "f"
	ListSearch   = "s"
)

// UserListQuery picks one page of a user list.
type UserListQuery struct {
	Kind   string
	Search string
	Offset int64
	Limit  int64
}

// UserService exposes the user operations the bot needs. Implementations must
// enforce the same permissions/limits as the HTTP API, and Get must only find
// users the actor may see.
type UserService interface {
	Get(ctx context.Context, actor Actor, username string) (UserView, error)
	Create(ctx context.Context, actor Actor, user NewUser) error
	// Renew adds days to the user's time and gigabytes to its data limit;
	// either may be zero.
	Renew(ctx context.Context, actor Actor, username string, days int64, gigabytes int64) error
	Services(ctx context.Context, actor Actor) ([]ServiceOption, error)
	// List returns one page of the users the actor may see and the total
	// that match.
	List(ctx context.Context, actor Actor, query UserListQuery) ([]UserView, int64, error)
	Delete(ctx context.Context, actor Actor, username string) error
	Reset(ctx context.Context, actor Actor, username string) error
	RevokeSubscription(ctx context.Context, actor Actor, username string) error
	SetStatus(ctx context.Context, actor Actor, username string, status string) error
	SetNote(ctx context.Context, actor Actor, username string, note string) error
}

// SystemInfo is the data the bot renders for the system status command.
type SystemInfo struct {
	Version     string
	CPUPercent  float64
	MemUsed     int64
	MemTotal    int64
	TotalUsers  int64
	ActiveUsers int64
	OnlineUsers int64
}

// SystemService exposes read-only system information for the bot.
type SystemService interface {
	Info(ctx context.Context) (SystemInfo, error)
}

// BackupStatus is the data the bot renders for the /backup command.
type BackupStatus struct {
	Enabled       bool
	Scope         string
	IntervalValue int
	IntervalUnit  string
	LastSentAt    *string
	LastError     *string
}

// BackupSendResult is the outcome of an on-demand backup delivery.
type BackupSendResult struct {
	Filename string
	Size     int64
}

// BackupRestoreResult is the outcome of restoring an uploaded backup archive.
type BackupRestoreResult struct {
	TablesRestored   int
	RowsRestored     int
	FilesRestored    []string
	Warnings         []string
	SafetyBackupPath string
}

// BackupService exposes the backup operations the bot needs: reading/changing
// the periodic-backup schedule, sending an on-demand backup, and restoring an
// uploaded archive. Implementations must enforce the same runtime/permission
// checks as the HTTP API (e.g. binary-install-only).
type BackupService interface {
	Status(ctx context.Context) (BackupStatus, error)
	SetSchedule(ctx context.Context, value int, unit string) error
	SendNow(ctx context.Context) (BackupSendResult, error)
	Restore(ctx context.Context, archivePath string) (BackupRestoreResult, error)
}
