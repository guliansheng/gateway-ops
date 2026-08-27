package storage

import (
	"time"

	"gorm.io/gorm"
)

// ChannelType 上游渠道类型。
type ChannelType string

const (
	ChannelTypeNewAPI  ChannelType = "newapi"
	ChannelTypeSub2API ChannelType = "sub2api"
)

// CredentialMode 渠道凭据模式：
//   - password: 经典模式，存账号 + 密码，由 Connector 走完整登录流程
//   - token:    跳过登录，存用户已有的 cookie / access_token，直接构造 AuthSession
//
// token 模式不依赖打码 / 不会自动续期，token 失效时表现为 last_error 显示鉴权失败。
type CredentialMode string

const (
	CredentialModePassword CredentialMode = "password"
	CredentialModeToken    CredentialMode = "token"
)

// BalanceMode controls whether balance data is read from the upstream or
// maintained from the locally attributed relay cost ledger.
type BalanceMode string

const (
	BalanceModeAuto   BalanceMode = "auto"
	BalanceModeManual BalanceMode = "manual"
)

// Channel 上游渠道账号。Password / Turnstile API key 等敏感字段都加密保存。
//
// 注意：会话凭据（access_token / cookie / csrf）单独存放在 AuthSession 表。
//
// CredentialMode + PasswordCipher 的语义重载：
//   - password 模式（默认）：Username + PasswordCipher 存账号密码，由 Connector.Login 用
//   - token    模式：PasswordCipher 存 JSON blob（NewAPI: {cookie,user_id} / Sub2API: {access_token}），
//     channel.Service 解析后直接构造 AuthSession，跳过 Login。Username 字段在 token 模式下保留
//     用户填写的备注（一般是邮箱），仅做展示。
//
// 复用 PasswordCipher 而不新增 TokenCipher 是为了让现有的 GORM 行 / 加密路径 / 迁移流程零变动。
type Channel struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	Name           string         `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Type           ChannelType    `gorm:"size:32;not null;index" json:"type"`
	SiteURL        string         `gorm:"size:512;not null" json:"site_url"`
	Username       string         `gorm:"size:256;not null" json:"username"`
	PasswordCipher string         `gorm:"size:4096;not null" json:"-"`
	CredentialMode CredentialMode `gorm:"size:16;not null;default:'password'" json:"credential_mode"`
	BalanceMode    BalanceMode    `gorm:"size:16;not null;default:'auto'" json:"balance_mode"`
	ManualBalance  float64        `gorm:"not null;default:0" json:"manual_balance"`
	// ManualUsageBaseline is the cumulative relay cost last settled against a
	// manually managed balance. It is intentionally internal bookkeeping and
	// not part of the public channel API.
	ManualUsageBaseline *float64 `gorm:"type:double precision" json:"-"`
	Remark              string   `gorm:"size:512" json:"remark,omitempty"`
	TurnstileEnabled    bool     `gorm:"default:false" json:"turnstile_enabled"`
	CaptchaConfigID     *uint    `json:"captcha_config_id,omitempty"`
	BalanceThreshold    float64  `gorm:"default:0" json:"balance_threshold"`
	MonitorEnabled      bool     `gorm:"default:true" json:"monitor_enabled"`

	// 最近一次采集结果（聚合视图，便于列表页直接展示）
	LastBalance          *float64   `json:"last_balance,omitempty"`
	LastBalanceAt        *time.Time `json:"last_balance_at,omitempty"`
	LastError            string     `gorm:"type:text" json:"last_error,omitempty"`
	LatestRatioChangedAt *time.Time `gorm:"->;-:migration;column:latest_ratio_changed_at" json:"latest_ratio_changed_at,omitempty"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// AuthSessionKey is only used while an additional account is resolved. It
	// keeps its cached session separate from the channel's primary account.
	AuthSessionKey uint `gorm:"-" json:"-"`
}

func (Channel) TableName() string { return "channels" }

// AdditionalAccountSessionOffset reserves an AuthSession key-space for
// additional channel accounts. The legacy primary account continues using the
// channel ID, preserving all existing cached sessions.
const AdditionalAccountSessionOffset uint = 1 << 32

func AdditionalAccountSessionKey(accountID uint) uint {
	return AdditionalAccountSessionOffset + accountID
}

// ChannelAccount is one login under an automatically monitored channel. The
// parent Channel owns shared policy, rate snapshots, and aggregate balance;
// every account stores its own encrypted credential and most recent balance.
type ChannelAccount struct {
	ID        uint `gorm:"primaryKey" json:"id"`
	ChannelID uint `gorm:"not null;index" json:"-"`
	IsPrimary bool `gorm:"not null;default:false;index" json:"is_primary"`

	Username         string         `gorm:"size:256;not null" json:"username"`
	PasswordCipher   string         `gorm:"size:4096;not null" json:"-"`
	CredentialMode   CredentialMode `gorm:"size:16;not null;default:'password'" json:"credential_mode"`
	TurnstileEnabled bool           `gorm:"default:false" json:"turnstile_enabled"`
	CaptchaConfigID  *uint          `json:"captcha_config_id,omitempty"`

	LastBalance   *float64   `json:"last_balance,omitempty"`
	LastBalanceAt *time.Time `json:"last_balance_at,omitempty"`
	LastError     string     `gorm:"type:text" json:"last_error,omitempty"`

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (ChannelAccount) TableName() string { return "channel_accounts" }

// AuthSession 渠道登录后保存的凭据，按 ChannelID 一对一关联。
// *Cipher 字段都用 AES-GCM 加密；UserID 是上游账号 ID 字符串（非敏感），明文存放。
type AuthSession struct {
	ChannelID         uint       `gorm:"primaryKey" json:"channel_id"`
	UserID            string     `gorm:"size:64" json:"user_id,omitempty"`
	AccessTokenCipher string     `gorm:"type:text" json:"-"`
	CookieCipher      string     `gorm:"type:text" json:"-"`
	CSRFTokenCipher   string     `gorm:"size:1024" json:"-"`
	ExpiresAt         *time.Time `json:"expires_at,omitempty"`
	LastLoginAt       *time.Time `json:"last_login_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (AuthSession) TableName() string { return "auth_sessions" }

// CaptchaProviderType 打码平台类型。
type CaptchaProviderType string

const (
	CaptchaCapSolver   CaptchaProviderType = "capsolver"
	CaptchaTwoCaptcha  CaptchaProviderType = "2captcha"
	CaptchaAntiCaptcha CaptchaProviderType = "anticaptcha"
	CaptchaYesCaptcha  CaptchaProviderType = "yescaptcha"
)

// CaptchaConfig 打码平台配置。APIKeyCipher 加密保存，Extra 存放各平台差异化 JSON。
type CaptchaConfig struct {
	ID           uint                `gorm:"primaryKey" json:"id"`
	Name         string              `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Type         CaptchaProviderType `gorm:"size:32;not null;index" json:"type"`
	APIKeyCipher string              `gorm:"size:1024" json:"-"`
	Endpoint     string              `gorm:"size:512" json:"endpoint,omitempty"`
	Extra        string              `gorm:"type:text" json:"extra,omitempty"`
	Enabled      bool                `gorm:"default:true" json:"enabled"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
	DeletedAt    gorm.DeletedAt      `gorm:"index" json:"-"`
}

func (CaptchaConfig) TableName() string { return "captcha_configs" }

const (
	RateSnapshotSourceUpstream     = "upstream"
	RateSnapshotSourceManual       = "manual"
	RateSnapshotSourceRelayAccount = "relay_account"
)

// RateSnapshot 渠道当前观察到的模型 / 分组倍率快照。upsert per (channel_id, model_name)。
// 实际的"变化历史"在 RateChangeLog；此表只保存当前状态。
type RateSnapshot struct {
	ID              uint    `gorm:"primaryKey" json:"id"`
	ChannelID       uint    `gorm:"not null;uniqueIndex:idx_rate_chan_model" json:"channel_id"`
	ModelName       string  `gorm:"size:256;not null;uniqueIndex:idx_rate_chan_model" json:"model_name"`
	Description     string  `gorm:"size:512" json:"description,omitempty"`
	Ratio           float64 `gorm:"not null" json:"ratio"`
	CompletionRatio float64 `json:"completion_ratio"`
	// Source distinguishes an upstream group, a user-maintained group, and a
	// group mirrored from a relay account. The latter is read-only because its
	// name and multiplier follow the account snapshot.
	Source                 string `gorm:"size:32;not null;default:'upstream';index" json:"source"`
	RelayStationID         *uint  `gorm:"index" json:"relay_station_id,omitempty"`
	RelayAccountExternalID *int64 `gorm:"index" json:"relay_account_external_id,omitempty"`

	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

func (RateSnapshot) TableName() string { return "rate_snapshots" }

// RateChangeLog 倍率变化历史。每次扫描发现差异时写入一行。
type RateChangeLog struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	ChannelID          uint      `gorm:"not null;index" json:"channel_id"`
	ModelName          string    `gorm:"size:256;not null;index" json:"model_name"`
	OldRatio           *float64  `json:"old_ratio,omitempty"`
	NewRatio           float64   `gorm:"not null" json:"new_ratio"`
	OldCompletionRatio *float64  `json:"old_completion_ratio,omitempty"`
	NewCompletionRatio float64   `json:"new_completion_ratio"`
	ChangedAt          time.Time `gorm:"not null;index" json:"changed_at"`
}

func (RateChangeLog) TableName() string { return "rate_change_logs" }

// BalanceSnapshot 周期性余额采样，用于图表展示。
type BalanceSnapshot struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ChannelID uint      `gorm:"not null;index" json:"channel_id"`
	Balance   float64   `gorm:"not null" json:"balance"`
	SampledAt time.Time `gorm:"not null;index" json:"sampled_at"`
}

func (BalanceSnapshot) TableName() string { return "balance_snapshots" }

// ChannelDailyBalance records the channel balance at the beginning of each
// local calendar day. The scheduler writes one idempotent row per channel/day;
// CapturedAt keeps the actual write time when a missed midnight is recovered
// after a restart.
type ChannelDailyBalance struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	ChannelID  uint      `gorm:"not null;uniqueIndex:idx_channel_daily_balance" json:"channel_id"`
	Day        time.Time `gorm:"type:date;not null;uniqueIndex:idx_channel_daily_balance" json:"day"`
	Balance    float64   `gorm:"not null" json:"balance"`
	CapturedAt time.Time `gorm:"not null;index" json:"captured_at"`
}

func (ChannelDailyBalance) TableName() string { return "channel_daily_balances" }

// BalanceChangeLog records the delta between two consecutive balance
// snapshots. Positive deltas are upstream recharges; negative deltas are
// upstream consumption. BalanceSnapshotID makes backfills idempotent.
type BalanceChangeLog struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	ChannelID         uint      `gorm:"not null;index" json:"channel_id"`
	BalanceSnapshotID uint      `gorm:"not null;uniqueIndex" json:"balance_snapshot_id"`
	PreviousBalance   float64   `gorm:"not null" json:"previous_balance"`
	NewBalance        float64   `gorm:"not null" json:"new_balance"`
	Delta             float64   `gorm:"not null" json:"delta"`
	Kind              string    `gorm:"size:24;not null;index" json:"kind"`
	DetectedAt        time.Time `gorm:"not null;index" json:"detected_at"`
}

func (BalanceChangeLog) TableName() string { return "balance_change_logs" }

// NotificationChannelType 通知渠道类型。第一版至少 telegram，其它预留。
type NotificationChannelType string

const (
	NotifyTelegram NotificationChannelType = "telegram"
	NotifyWebhook  NotificationChannelType = "webhook"
	NotifyEmail    NotificationChannelType = "email"
	NotifyWecom    NotificationChannelType = "wecom"
	NotifyDingTalk NotificationChannelType = "dingtalk"
	NotifyFeishu   NotificationChannelType = "feishu"
	NotifyBark     NotificationChannelType = "bark"
)

// NotificationChannel 通知渠道配置。ConfigCipher 加密保存 JSON 配置（含 token / webhook url / 密码等）。
//
// Subscriptions 是 JSON 数组，记录该渠道关心的上游 + 分组过滤；为空 / "[]" 表示订阅一切。
// 非敏感数据，明文保存，方便 Dispatcher 直接读取过滤而不解密。
type NotificationChannel struct {
	ID            uint                    `gorm:"primaryKey" json:"id"`
	Name          string                  `gorm:"size:128;not null;uniqueIndex" json:"name"`
	Type          NotificationChannelType `gorm:"size:32;not null;index" json:"type"`
	ConfigCipher  string                  `gorm:"type:text;not null" json:"-"`
	Subscriptions string                  `gorm:"type:text;not null;default:'[]'" json:"subscriptions"`
	Enabled       bool                    `gorm:"default:true" json:"enabled"`
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
	DeletedAt     gorm.DeletedAt          `gorm:"index" json:"-"`
}

func (NotificationChannel) TableName() string { return "notification_channels" }

// NotificationEvent 系统内部触发的通知事件类型。
type NotificationEvent string

const (
	EventBalanceLow    NotificationEvent = "balance_low"
	EventRateChanged   NotificationEvent = "rate_changed"
	EventLoginFailed   NotificationEvent = "login_failed"
	EventCaptchaFailed NotificationEvent = "captcha_failed"
	EventMonitorFailed NotificationEvent = "monitor_failed"
)

// NotificationLog 通知发送记录。
type NotificationLog struct {
	ID           uint              `gorm:"primaryKey" json:"id"`
	ChannelID    uint              `gorm:"not null;index" json:"channel_id"`
	Event        NotificationEvent `gorm:"size:64;not null;index" json:"event"`
	Subject      string            `gorm:"size:512;not null" json:"subject"`
	Body         string            `gorm:"type:text" json:"body"`
	Success      bool              `gorm:"not null" json:"success"`
	ErrorMessage string            `gorm:"type:text" json:"error_message,omitempty"`
	SentAt       time.Time         `gorm:"not null;index" json:"sent_at"`
}

func (NotificationLog) TableName() string { return "notification_logs" }

// NotificationCooldown 跨重启持久化的通知冷却记录。
//
// 业务键 (ChannelID, Event)：标记某渠道某类事件最近一次发送时间。
// Dispatcher 在发送 cooldown-aware 事件（如 balance_low）前查这张表，
// 命中且未过 cooldown 就跳过。
//
// 不和 NotificationLog 合并是因为：
//   - NotificationLog 是审计/历史日志（用户可见、可清理）
//   - NotificationCooldown 是去抖控制平面（仅最新一条、原子 upsert）
//
// ChannelID 这里指的是**上游渠道**（storage.Channel），不是通知渠道。
type NotificationCooldown struct {
	ChannelID  uint              `gorm:"primaryKey" json:"channel_id"`
	Event      NotificationEvent `gorm:"primaryKey;size:64" json:"event"`
	LastSentAt time.Time         `gorm:"not null" json:"last_sent_at"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

func (NotificationCooldown) TableName() string { return "notification_cooldowns" }

// MonitorJob 监控任务类型。
type MonitorJob string

const (
	MonitorJobLogin   MonitorJob = "login"
	MonitorJobBalance MonitorJob = "balance"
	MonitorJobRates   MonitorJob = "rates"
)

// MonitorLog 每次扫描 / 登录尝试的结果，便于诊断失败。
type MonitorLog struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	ChannelID    uint       `gorm:"not null;index" json:"channel_id"`
	Job          MonitorJob `gorm:"size:32;not null;index" json:"job"`
	Success      bool       `gorm:"not null" json:"success"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	DurationMS   int64      `json:"duration_ms"`
	StartedAt    time.Time  `gorm:"not null;index" json:"started_at"`
	FinishedAt   time.Time  `json:"finished_at"`
}

func (MonitorLog) TableName() string { return "monitor_logs" }

// AppSetting 保存可在管理界面调整的运行参数。
type AppSetting struct {
	Key       string    `gorm:"primaryKey;size:128" json:"key"`
	Value     string    `gorm:"type:text;not null" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (AppSetting) TableName() string { return "app_settings" }

// LocalAccount 是尚未投放或已经关联到中转站的本地账号资产。
// 这里只保存经营和生命周期所需的非敏感信息，不保存 token、cookie 或密码。
type LocalAccount struct {
	ID                     uint       `gorm:"primaryKey" json:"id"`
	Name                   string     `gorm:"size:256;not null" json:"name"`
	Identifier             string     `gorm:"size:256;not null;uniqueIndex:idx_local_account_identity" json:"identifier"`
	Platform               string     `gorm:"size:64;not null;uniqueIndex:idx_local_account_identity" json:"platform"`
	AccountType            string     `gorm:"size:64;not null;default:'oauth'" json:"account_type"`
	Status                 string     `gorm:"size:32;not null;default:'ready';index" json:"status"`
	PurchaseCost           float64    `gorm:"not null;default:0" json:"purchase_cost"`
	ExpectedQuota          float64    `gorm:"not null;default:0" json:"expected_quota"`
	PurchasedAt            time.Time  `gorm:"not null;index" json:"purchased_at"`
	ExpiresAt              *time.Time `json:"expires_at,omitempty"`
	Notes                  string     `gorm:"type:text" json:"notes,omitempty"`
	RelayStationID         *uint      `gorm:"index" json:"relay_station_id,omitempty"`
	RelayAccountExternalID *int64     `gorm:"index" json:"relay_account_external_id,omitempty"`
	LinkedAt               *time.Time `json:"linked_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

func (LocalAccount) TableName() string { return "local_accounts" }

// OperationLedgerEntry 是显式的经营收支事实。系统不再根据渠道余额变化推断充值；
// 本地账号采购成本由 LocalAccount 自动维护一条 source=local_account 的支出记录。
type OperationLedgerEntry struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	Direction      string    `gorm:"size:16;not null;index" json:"direction"`
	Category       string    `gorm:"size:64;not null;index" json:"category"`
	Amount         float64   `gorm:"not null" json:"amount"`
	Currency       string    `gorm:"size:8;not null;default:'CNY'" json:"currency"`
	Description    string    `gorm:"size:512;not null" json:"description"`
	Source         string    `gorm:"size:32;not null;default:'manual';index" json:"source"`
	ChannelID      *uint     `gorm:"index" json:"channel_id,omitempty"`
	RelayStationID *uint     `gorm:"index" json:"relay_station_id,omitempty"`
	LocalAccountID *uint     `gorm:"uniqueIndex" json:"local_account_id,omitempty"`
	OccurredAt     time.Time `gorm:"not null;index" json:"occurred_at"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (OperationLedgerEntry) TableName() string { return "operation_ledger_entries" }

// RelayStation 是要分析的 Sub2API 中转站。管理员 API Key 仅以密文保存，
// 不会从 API 返回给前端。
type RelayStation struct {
	ID                        uint           `gorm:"primaryKey" json:"id"`
	Name                      string         `gorm:"size:128;not null;uniqueIndex" json:"name"`
	BaseURL                   string         `gorm:"size:512;not null" json:"base_url"`
	APIKeyCipher              string         `gorm:"type:text;not null" json:"-"`
	AutoAdjustEnabled         bool           `gorm:"default:false" json:"auto_adjust_enabled"`
	AutoAdjustNoProfitEnabled bool           `gorm:"default:false" json:"auto_adjust_no_profit_enabled"`
	AutoPriorityEnabled       bool           `gorm:"default:false" json:"auto_priority_enabled"`
	AutoPriorityRecallEnabled bool           `gorm:"default:false" json:"auto_priority_recall_enabled"`
	AutoPriorityRecallMinutes int            `gorm:"not null;default:180" json:"auto_priority_recall_minutes"`
	LastSyncedAt              *time.Time     `json:"last_synced_at,omitempty"`
	LastError                 string         `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt                 time.Time      `json:"created_at"`
	UpdatedAt                 time.Time      `json:"updated_at"`
	DeletedAt                 gorm.DeletedAt `gorm:"index" json:"-"`
}

func (RelayStation) TableName() string { return "relay_stations" }

// RelayGroup 是从中转站管理端同步的销售分组快照。
type RelayGroup struct {
	ID               uint      `gorm:"primaryKey" json:"id"`
	RelayStationID   uint      `gorm:"not null;uniqueIndex:idx_relay_group_external" json:"relay_station_id"`
	ExternalID       int64     `gorm:"not null;uniqueIndex:idx_relay_group_external" json:"external_id"`
	Name             string    `gorm:"size:256;not null" json:"name"`
	Description      string    `gorm:"size:512" json:"description,omitempty"`
	Platform         string    `gorm:"size:64" json:"platform,omitempty"`
	Status           string    `gorm:"size:32" json:"status,omitempty"`
	IsExclusive      bool      `gorm:"not null;default:false" json:"is_exclusive"`
	RequireOAuthOnly bool      `json:"require_oauth_only"`
	SortOrder        int       `gorm:"not null;default:0" json:"sort_order"`
	ModelTypesJSON   string    `gorm:"type:text" json:"-"`
	MonitorEnabled   bool      `gorm:"not null;default:false" json:"monitor_enabled"`
	RateMultiplier   float64   `gorm:"not null" json:"rate_multiplier"`
	SyncedAt         time.Time `gorm:"not null;index" json:"synced_at"`
}

func (RelayGroup) TableName() string { return "relay_groups" }

// RelayChannel 是中转站里配置的渠道。GroupIDs 通过 RelayChannelGroup 关联。
type RelayChannel struct {
	ID                         uint       `gorm:"primaryKey" json:"id"`
	RelayStationID             uint       `gorm:"not null;uniqueIndex:idx_relay_channel_external" json:"relay_station_id"`
	ExternalID                 int64      `gorm:"not null;uniqueIndex:idx_relay_channel_external" json:"external_id"`
	Name                       string     `gorm:"size:256;not null" json:"name"`
	Description                string     `gorm:"size:512" json:"description,omitempty"`
	Status                     string     `gorm:"size:32" json:"status,omitempty"`
	BillingModelSource         string     `gorm:"size:64" json:"billing_model_source,omitempty"`
	ApplyPricingToAccountStats bool       `gorm:"default:false" json:"apply_pricing_to_account_stats"`
	PricingJSON                string     `gorm:"type:text" json:"-"`
	PricingHash                string     `gorm:"size:64" json:"-"`
	PricingModelsJSON          string     `gorm:"type:text" json:"-"`
	PricingModelCount          int        `gorm:"default:0" json:"pricing_model_count"`
	PricingRuleCount           int        `gorm:"default:0" json:"pricing_rule_count"`
	PricingChangedAt           *time.Time `json:"pricing_changed_at,omitempty"`
	SyncedAt                   time.Time  `gorm:"not null;index" json:"synced_at"`
}

func (RelayChannel) TableName() string { return "relay_channels" }

// RelayChannelGroup 保存中转站渠道与分组的多对多关系。
type RelayChannelGroup struct {
	RelayChannelID uint `gorm:"primaryKey;uniqueIndex:idx_relay_channel_group" json:"relay_channel_id"`
	RelayGroupID   uint `gorm:"primaryKey;uniqueIndex:idx_relay_channel_group" json:"relay_group_id"`
}

func (RelayChannelGroup) TableName() string { return "relay_channel_groups" }

// RelayChannelMapping 将中转站渠道显式对应到当前 Hub 的监测渠道和上游分组。
// 上游分组名为空表示尚未完成倍率对比配置。
type RelayChannelMapping struct {
	ID                     uint      `gorm:"primaryKey" json:"id"`
	RelayStationID         uint      `gorm:"not null;uniqueIndex:idx_relay_mapping_channel" json:"relay_station_id"`
	RelayChannelExternalID int64     `gorm:"not null;uniqueIndex:idx_relay_mapping_channel" json:"relay_channel_external_id"`
	MonitorChannelID       *uint     `gorm:"index" json:"monitor_channel_id,omitempty"`
	UpstreamGroup          string    `gorm:"size:256" json:"upstream_group,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (RelayChannelMapping) TableName() string { return "relay_channel_mappings" }

// RelayChannelPricingChange 记录中转站渠道定价配置的实际变化。
type RelayChannelPricingChange struct {
	ID                     uint      `gorm:"primaryKey" json:"id"`
	RelayStationID         uint      `gorm:"not null;index" json:"relay_station_id"`
	RelayChannelExternalID int64     `gorm:"not null;index" json:"relay_channel_external_id"`
	OldPricingJSON         string    `gorm:"type:text" json:"-"`
	NewPricingJSON         string    `gorm:"type:text" json:"-"`
	OldModelCount          int       `json:"old_model_count"`
	NewModelCount          int       `json:"new_model_count"`
	OldRuleCount           int       `json:"old_rule_count"`
	NewRuleCount           int       `json:"new_rule_count"`
	ChangedAt              time.Time `gorm:"not null;index" json:"changed_at"`
}

func (RelayChannelPricingChange) TableName() string { return "relay_channel_pricing_changes" }

// RelayAccount 是从 Sub2API 管理端读取的实际上游账号。RateMultiplier 是该账号
// 的成本倍率；GroupIDs 通过 RelayAccountGroup 关联到销售分组。
type RelayAccount struct {
	ID                 uint   `gorm:"primaryKey" json:"id"`
	RelayStationID     uint   `gorm:"not null;uniqueIndex:idx_relay_account_external" json:"relay_station_id"`
	ExternalID         int64  `gorm:"not null;uniqueIndex:idx_relay_account_external" json:"external_id"`
	Name               string `gorm:"size:256;not null" json:"name"`
	BaseURL            string `gorm:"size:1024" json:"base_url,omitempty"`
	Platform           string `gorm:"size:64" json:"platform,omitempty"`
	Type               string `gorm:"size:64" json:"type,omitempty"`
	Status             string `gorm:"size:32" json:"status,omitempty"`
	Schedulable        bool   `json:"schedulable"`
	Concurrency        int    `gorm:"not null;default:3" json:"concurrency"`
	CurrentConcurrency int    `gorm:"not null;default:0" json:"current_concurrency"`
	Priority           int    `gorm:"not null;default:50" json:"priority"`
	PoolMode           bool   `gorm:"not null;default:false" json:"pool_mode"`
	PoolModeRetryCount int    `gorm:"not null;default:3" json:"pool_mode_retry_count"`
	AccountPlan        string `gorm:"size:64" json:"account_plan,omitempty"`
	ModelType          string `gorm:"size:64" json:"model_type,omitempty"`
	// This value is populated only from extra.upstream_billing_probe. The
	// remote accounts.rate_multiplier defaults to 1 and is not upstream cost.
	RateMultiplier *float64   `json:"rate_multiplier,omitempty"`
	RateSource     string     `gorm:"size:32" json:"rate_source,omitempty"`
	RateObservedAt *time.Time `json:"rate_observed_at,omitempty"`
	// LatencySamplesJSON stores the latest Sub2API usage samples as a compact
	// snapshot so the risk table never has to fan out requests per account.
	LatencySamplesJSON string     `gorm:"type:text" json:"-"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	SyncedAt           time.Time  `gorm:"not null;index" json:"synced_at"`
}

func (RelayAccount) TableName() string { return "relay_accounts" }

// RelayLatencySample is the small, stable subset of a Sub2API usage row that
// is useful for the account health table.
type RelayLatencySample struct {
	FirstTokenMS int64     `json:"first_token_ms"`
	DurationMS   int64     `json:"duration_ms"`
	CreatedAt    time.Time `json:"created_at"`
	Model        string    `json:"model,omitempty"`
	RequestType  string    `json:"request_type,omitempty"`
	UserEmail    string    `json:"user_email,omitempty"`
}

// RelayAccountGroup 保存上游账号与销售分组的关联。
type RelayAccountGroup struct {
	RelayAccountID uint `gorm:"primaryKey;uniqueIndex:idx_relay_account_group" json:"relay_account_id"`
	RelayGroupID   uint `gorm:"primaryKey;uniqueIndex:idx_relay_account_group" json:"relay_group_id"`
}

func (RelayAccountGroup) TableName() string { return "relay_account_groups" }

// RelayAccountCostOverride 是账号级成本覆盖。它不引用 relay_accounts 的本地
// 自增 ID，因为同步会重建快照；业务键是中转站和远端账号 ID。
type RelayAccountCostOverride struct {
	ID                     uint      `gorm:"primaryKey" json:"id"`
	RelayStationID         uint      `gorm:"not null;uniqueIndex:idx_relay_account_cost_override" json:"relay_station_id"`
	RelayAccountExternalID int64     `gorm:"not null;uniqueIndex:idx_relay_account_cost_override" json:"relay_account_external_id"`
	Mode                   string    `gorm:"size:24;not null" json:"mode"` // channel_group / manual / auto_link
	MonitorChannelID       *uint     `gorm:"index" json:"monitor_channel_id,omitempty"`
	UpstreamGroup          string    `gorm:"size:256" json:"upstream_group,omitempty"`
	ManualMultiplier       *float64  `json:"manual_multiplier,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

func (RelayAccountCostOverride) TableName() string { return "relay_account_cost_overrides" }

// RelayAccountAdjustmentLog 审计账号的手动和自动调整。分组 ID 保留为远端 ID，
// 从而即使后续快照重建也能还原一次实际写入；运行参数使用可空字段兼容历史记录。
type RelayAccountAdjustmentLog struct {
	ID                     uint      `gorm:"primaryKey" json:"id"`
	RelayStationID         uint      `gorm:"not null;index" json:"relay_station_id"`
	RelayAccountExternalID int64     `gorm:"not null;index" json:"relay_account_external_id"`
	AccountName            string    `gorm:"size:256" json:"account_name"`
	AccountPlatform        string    `gorm:"size:64" json:"account_platform"`
	CostMultiplier         float64   `gorm:"not null" json:"cost_multiplier"`
	OldGroupIDsJSON        string    `gorm:"type:text;not null" json:"-"`
	NewGroupIDsJSON        string    `gorm:"type:text;not null" json:"-"`
	RecommendedGroupID     *int64    `json:"recommended_group_id,omitempty"`
	OldConcurrency         *int      `json:"old_concurrency,omitempty"`
	NewConcurrency         *int      `json:"new_concurrency,omitempty"`
	OldPriority            *int      `json:"old_priority,omitempty"`
	NewPriority            *int      `json:"new_priority,omitempty"`
	OldPoolModeRetryCount  *int      `json:"old_pool_mode_retry_count,omitempty"`
	NewPoolModeRetryCount  *int      `json:"new_pool_mode_retry_count,omitempty"`
	Source                 string    `gorm:"size:16;not null" json:"source"`
	Action                 string    `gorm:"size:32;not null;default:'group_update'" json:"action"`
	Success                bool      `gorm:"not null" json:"success"`
	ErrorMessage           string    `gorm:"type:text" json:"error_message,omitempty"`
	AppliedAt              time.Time `gorm:"not null;index" json:"applied_at"`
}

func (RelayAccountAdjustmentLog) TableName() string { return "relay_account_adjustment_logs" }

// SyncSettings 是管理界面的自动同步设置。
type SyncSettings struct {
	ChannelConfigured            bool `json:"channel_configured"`
	ChannelEnabled               bool `json:"channel_enabled"`
	ChannelIntervalMinutes       int  `json:"channel_interval_minutes"`
	RelayRateConfigured          bool `json:"relay_rate_configured"`
	RelayRateEnabled             bool `json:"relay_rate_enabled"`
	RelayRateIntervalMinutes     int  `json:"relay_rate_interval_minutes"`
	RelaySnapshotConfigured      bool `json:"relay_snapshot_configured"`
	RelaySnapshotEnabled         bool `json:"relay_snapshot_enabled"`
	RelaySnapshotIntervalMinutes int  `json:"relay_snapshot_interval_minutes"`
	RelaySnapshotIntervalSeconds int  `json:"relay_snapshot_interval_seconds"`
}
