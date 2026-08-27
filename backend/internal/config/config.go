package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Database      DatabaseConfig      `mapstructure:"database"`
	Security      SecurityConfig      `mapstructure:"security"`
	Auth          AuthConfig          `mapstructure:"auth"`
	Scheduler     SchedulerConfig     `mapstructure:"scheduler"`
	Notifications NotificationsConfig `mapstructure:"notifications"`
	Log           LogConfig           `mapstructure:"log"`
}

type ServerConfig struct {
	Port           int      `mapstructure:"port"`
	Mode           string   `mapstructure:"mode"`
	TrustedProxies []string `mapstructure:"trustedProxies"`
	BaseURL        string   `mapstructure:"baseURL"`
}

type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Name         string `mapstructure:"name"`
	SSLMode      string `mapstructure:"sslMode"`
	Timezone     string `mapstructure:"timezone"`
	MaxOpenConns int    `mapstructure:"maxOpenConns"`
	MaxIdleConns int    `mapstructure:"maxIdleConns"`
}

func (d DatabaseConfig) ToStorageConfig() storage.DBConfig {
	return storage.DBConfig{
		Host:         d.Host,
		Port:         d.Port,
		User:         d.User,
		Password:     d.Password,
		Name:         d.Name,
		SSLMode:      d.SSLMode,
		Timezone:     d.Timezone,
		MaxOpenConns: d.MaxOpenConns,
		MaxIdleConns: d.MaxIdleConns,
	}
}

type SecurityConfig struct {
	// AppSecret 主密钥，用于 AES-GCM。优先从 APP_SECRET 环境变量读取。
	AppSecret string `mapstructure:"appSecret"`
}

// AuthConfig 后台单用户登录配置。
//
// Enabled = false（默认）时整套鉴权被关掉：/api/* 全部免 token，前端检测后跳过登录页。
// 适合纯内网 / 反代后面的部署。需要公网暴露时必须显式 Enabled=true 并设强密码。
//
// Enabled=true 时 Username/Password 是写死的管理员凭据，TokenSecret 用于签发 HMAC token。
// 如果 TokenSecret 为空，会回退使用 Security.AppSecret，保证有合理默认。
type AuthConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	TokenSecret     string `mapstructure:"tokenSecret"`
	SessionTTLHours int    `mapstructure:"sessionTTLHours"`
}

type SchedulerConfig struct {
	BalanceCron string          `mapstructure:"balanceCron"`
	RateCron    string          `mapstructure:"rateCron"`
	Concurrency int             `mapstructure:"concurrency"`
	Retention   RetentionConfig `mapstructure:"retention"`
}

// RetentionConfig 历史数据保留策略。
//
// 字段为 0 表示该表不清理，永久保留（默认 rate_change_logs 永远保留，是核心业务数据）。
// Cron 为空时不启动清理任务。
type RetentionConfig struct {
	Cron                 string `mapstructure:"cron"`
	MonitorLogsDays      int    `mapstructure:"monitorLogsDays"`
	BalanceSnapshotsDays int    `mapstructure:"balanceSnapshotsDays"`
	NotificationLogsDays int    `mapstructure:"notificationLogsDays"`
}

// NotificationsConfig 通知去抖策略。所有字段都是"少烦我"取向，默认不丢消息只合并。
//
//   - BatchRateChanges：同次扫描中将多个分组的变化合并成 1 条消息，避免上游一次大调价
//     瞬间发出 30+ 条通知刷屏。默认 true。
//   - MinChangePct：涨跌幅 < X% 的 rate_changed 跳过推送（仍会写入 rate_change_logs）。
//     0 = 全发，对应原始行为。
//   - BalanceLowCooldownMinutes：同一渠道的 balance_low 在 X 分钟内不重复推送。
//     0 = 不冷却（每次扫描发现仍 < 阈值都发）。冷却状态持久化在 PostgreSQL 的
//     notification_cooldowns 表，跨重启生效。
//   - SendMaxAttempts：单条通知发送失败时最多尝试次数（含首次）。
//     1 = 不重试。重试采用指数退避：1s / 2s / 4s …，上限 30s。
type NotificationsConfig struct {
	BatchRateChanges          bool    `mapstructure:"batchRateChanges"`
	MinChangePct              float64 `mapstructure:"minChangePct"`
	BalanceLowCooldownMinutes int     `mapstructure:"balanceLowCooldownMinutes"`
	SendMaxAttempts           int     `mapstructure:"sendMaxAttempts"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Load 读取 config.yaml（可选）+ APP_SECRET / GATEWAYOPS_* 环境变量覆盖。
//
// 关键映射：
//
//	APP_SECRET                       -> security.appSecret
//	GATEWAYOPS_DATABASE_HOST          -> database.host
//	GATEWAYOPS_SERVER_PORT            -> server.port
//	GATEWAYOPS_SCHEDULER_BALANCECRON  -> scheduler.balanceCron
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("yaml")

	if path != "" {
		v.SetConfigFile(path)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(".")
		v.AddConfigPath("./backend")
		v.AddConfigPath("/etc/gateway-ops")
	}

	setDefaults(v)

	v.SetEnvPrefix("GATEWAYOPS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	// APP_SECRET / ADMIN_USERNAME / ADMIN_PASSWORD / AUTH_ENABLED 是独立约定的环境变量名，不带前缀。
	_ = v.BindEnv("security.appSecret", "APP_SECRET")
	_ = v.BindEnv("auth.enabled", "AUTH_ENABLED")
	_ = v.BindEnv("auth.username", "ADMIN_USERNAME")
	_ = v.BindEnv("auth.password", "ADMIN_PASSWORD")
	_ = v.BindEnv("auth.tokenSecret", "AUTH_TOKEN_SECRET")
	// Viper 坑：AutomaticEnv 只对已通过 SetDefault / BindEnv / 配置文件注册过的 key 生效；
	// 数据库的 user/password/name 没有合理的默认值（拒绝写"change-me"作默认），
	// 因此显式 BindEnv 以确保从环境变量读取。
	bindEnv(v, "database.host", "GATEWAYOPS_DATABASE_HOST")
	bindEnv(v, "database.port", "GATEWAYOPS_DATABASE_PORT")
	bindEnv(v, "database.user", "GATEWAYOPS_DATABASE_USER")
	bindEnv(v, "database.password", "GATEWAYOPS_DATABASE_PASSWORD")
	bindEnv(v, "database.name", "GATEWAYOPS_DATABASE_NAME")
	bindEnv(v, "database.sslMode", "GATEWAYOPS_DATABASE_SSLMODE")
	bindEnv(v, "server.port", "GATEWAYOPS_SERVER_PORT")
	bindEnv(v, "server.mode", "GATEWAYOPS_SERVER_MODE")
	bindEnv(v, "log.level", "GATEWAYOPS_LOG_LEVEL")
	bindEnv(v, "scheduler.balanceCron", "GATEWAYOPS_SCHEDULER_BALANCECRON")
	bindEnv(v, "scheduler.rateCron", "GATEWAYOPS_SCHEDULER_RATECRON")
	bindEnv(v, "scheduler.concurrency", "GATEWAYOPS_SCHEDULER_CONCURRENCY")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	// Viper's Unmarshal only considers keys present in its settings map. Values
	// supplied exclusively through BindEnv (without a default/config-file value)
	// would otherwise be omitted from this nested struct.
	if value, ok := os.LookupEnv("GATEWAYOPS_DATABASE_USER"); ok {
		cfg.Database.User = value
	}
	if value, ok := os.LookupEnv("GATEWAYOPS_DATABASE_PASSWORD"); ok {
		cfg.Database.Password = value
	}
	if value, ok := os.LookupEnv("GATEWAYOPS_DATABASE_NAME"); ok {
		cfg.Database.Name = value
	}
	return cfg, nil
}

func bindEnv(v *viper.Viper, key, name string) {
	_ = v.BindEnv(key, name)
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.port", 8418)
	v.SetDefault("server.mode", "debug")
	v.SetDefault("server.baseURL", "http://localhost:8418")

	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 54329)
	v.SetDefault("database.sslMode", "disable")
	v.SetDefault("database.timezone", "Asia/Shanghai")
	v.SetDefault("database.maxOpenConns", 20)
	v.SetDefault("database.maxIdleConns", 5)

	// CLAUDE.md 默认建议：余额 15 分钟，倍率 30 分钟。
	v.SetDefault("scheduler.balanceCron", "37 */15 * * * *")
	v.SetDefault("scheduler.rateCron", "13 */30 * * * *")
	v.SetDefault("scheduler.concurrency", 4)

	// 历史清理：每天凌晨 3:17 跑一次（6 字段 cron 含秒），
	// monitor 30 天 / balance 90 天 / notify 90 天。rate_change_logs 不清理（业务核心数据）。
	v.SetDefault("scheduler.retention.cron", "0 17 3 * * *")
	v.SetDefault("scheduler.retention.monitorLogsDays", 30)
	v.SetDefault("scheduler.retention.balanceSnapshotsDays", 90)
	v.SetDefault("scheduler.retention.notificationLogsDays", 90)

	v.SetDefault("auth.enabled", false)
	v.SetDefault("auth.username", "admin")
	v.SetDefault("auth.sessionTTLHours", 168) // 7 天

	// 通知去抖：默认开合并、不过滤涨跌幅、balance_low 1h 内不重复、失败重试 3 次。
	// 即"默认行为是合并刷屏 + 不重复 balance_low + 抗短时网络抖动"，不丢任何 rate_changed 事件。
	v.SetDefault("notifications.batchRateChanges", true)
	v.SetDefault("notifications.minChangePct", 0)
	v.SetDefault("notifications.balanceLowCooldownMinutes", 60)
	v.SetDefault("notifications.sendMaxAttempts", 3)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "text")
}
