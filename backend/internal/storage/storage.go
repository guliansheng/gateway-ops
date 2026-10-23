package storage

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type DBConfig struct {
	Host         string
	Port         int
	User         string
	Password     string
	Name         string
	SSLMode      string
	Timezone     string
	MaxOpenConns int
	MaxIdleConns int
}

func (c DBConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode, c.Timezone,
	)
}

// newGormLogger 关掉 GORM 默认 logger 对 ErrRecordNotFound 的告警噪音。
//
// 业务代码（如 Rates.Upsert）显式处理了"找不到就插入"，这种情况下 GORM 默认仍会
// 把 record not found 当 Warn 打出来，造成日志看起来满是错误其实没问题。
// IgnoreRecordNotFoundError = true 可以静默这类预期内的"未找到"。
func newGormLogger() logger.Interface {
	return logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		},
	)
}

func Open(cfg DBConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{
		Logger: newGormLogger(),
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	return db, nil
}

// AutoMigrate 启动时自动同步表结构。生产环境也提供 migrations/*.sql 作为对照。
func AutoMigrate(db *gorm.DB) error {
	// Platform-level fallback bindings were removed in favor of account-level
	// overrides. Drop the obsolete table even when upgrading an existing DB.
	if err := db.Exec("DROP TABLE IF EXISTS relay_cost_fallbacks").Error; err != nil {
		return err
	}
	for _, column := range []string{"min_margin", "max_auto_adjustments_per_sync"} {
		if db.Migrator().HasColumn(&RelayStation{}, column) {
			if err := db.Migrator().DropColumn(&RelayStation{}, column); err != nil {
				return err
			}
		}
	}
	if db.Migrator().HasColumn(&RelayAccountAdjustmentLog{}, "min_margin") {
		if err := db.Migrator().DropColumn(&RelayAccountAdjustmentLog{}, "min_margin"); err != nil {
			return err
		}
	}
	if db.Migrator().HasColumn(&RelayGroup{}, "is_exclusive") {
		if err := db.Exec("UPDATE relay_groups SET is_exclusive = false WHERE is_exclusive IS NULL").Error; err != nil {
			return err
		}
	}
	// Add and backfill the local pricing toggle only when its column is absent.
	// This must run before AutoMigrate so later startups preserve manual false values.
	if err := migrateGroupModelPricing(db); err != nil {
		return err
	}
	if err := migrateDegradationEffortDefaults(db); err != nil {
		return err
	}
	if err := migrateDegradationMethodConstraint(db); err != nil {
		return err
	}
	// 帮助文档旧结构（Markdown + 多站发布配置）先升级为“每站一篇 HTML 文档”，
	// 必须在 GORM AutoMigrate 之前处理 markdown → content 的列改名。
	if err := migrateHelpSchema(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(
		&Channel{},
		&ChannelAccount{},
		&AuthSession{},
		&CaptchaConfig{},
		&RateSnapshot{},
		&RateChangeLog{},
		&BalanceSnapshot{},
		&ChannelDailyBalance{},
		&BalanceChangeLog{},
		&NotificationChannel{},
		&NotificationLog{},
		&NotificationCooldown{},
		&MonitorLog{},
		&AppSetting{},
		&HelpDocument{},
		&HelpComment{},
		&LocalAccount{},
		&OperationLedgerEntry{},
		&RelayStation{},
		&RelayGroup{},
		&GroupDegradationConfig{},
		&GroupDegradationResult{},
		&DegradationPromptSettings{},
		&AccountDegradationRun{},
		&AccountDegradationResult{},
		&AccountDegradationConfig{},
		&RelayChannel{},
		&RelayChannelGroup{},
		&RelayChannelMapping{},
		&RelayChannelPricingChange{},
		&RelayAccount{},
		&RelayAccountGroup{},
		&RelayAccountCostOverride{},
		&RelayAccountAdjustmentLog{},
	); err != nil {
		return err
	}
	// Keep the existing channel row as the durable owner of monitoring history,
	// then project every automatic channel's legacy credential into its primary
	// account record. Existing AuthSession rows remain keyed by channel ID.
	if err := db.Exec(`
		INSERT INTO channel_accounts
			(channel_id, is_primary, username, password_cipher, credential_mode,
			 turnstile_enabled, captcha_config_id, last_balance, last_balance_at,
			 last_error, created_at, updated_at)
		SELECT c.id, true, c.username, c.password_cipher, c.credential_mode,
			c.turnstile_enabled, c.captcha_config_id, c.last_balance, c.last_balance_at,
			c.last_error, c.created_at, c.updated_at
		FROM channels c
		WHERE c.balance_mode = 'auto'
		  AND c.deleted_at IS NULL
		  AND NOT EXISTS (
				SELECT 1 FROM channel_accounts a
				WHERE a.channel_id = c.id AND a.is_primary = true AND a.deleted_at IS NULL
		  )
	`).Error; err != nil {
		return err
	}
	// Channel rows are soft-deleted so their original encrypted credential is
	// retained for audit. Do not duplicate that credential into account rows for
	// retired channels, including rows created by an earlier startup migration.
	if err := db.Exec(`
		DELETE FROM channel_accounts a
		USING channels c
		WHERE a.channel_id = c.id AND c.deleted_at IS NOT NULL
	`).Error; err != nil {
		return err
	}
	// Channel names only need to be unique among live channels. The original
	// unqualified unique index also reserved names from soft-deleted history,
	// making a hidden retired channel block a legitimate rename.
	if err := db.Exec("DROP INDEX IF EXISTS idx_channels_name").Error; err != nil {
		return err
	}
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_channels_name ON channels (name) WHERE deleted_at IS NULL").Error; err != nil {
		return err
	}
	// Existing installations already have balance snapshots. Seed the daily
	// table with the first observed balance of each historical calendar day.
	// Future rows are captured at midnight by the scheduler.
	if err := db.Exec(`
		INSERT INTO channel_daily_balances (channel_id, day, balance, captured_at)
		SELECT DISTINCT ON (channel_id, date_trunc('day', sampled_at))
			channel_id, CAST(date_trunc('day', sampled_at) AS date), balance, sampled_at
		FROM balance_snapshots
		ORDER BY channel_id, date_trunc('day', sampled_at), sampled_at ASC, id ASC
		ON CONFLICT (channel_id, day) DO NOTHING
	`).Error; err != nil {
		return err
	}
	// Convert every
	// consecutive non-zero delta into an idempotent change record so dashboard
	// ranges remain useful immediately after this feature is deployed.
	if err := db.Exec(`
		INSERT INTO balance_change_logs
			(channel_id, balance_snapshot_id, previous_balance, new_balance, delta, kind, detected_at)
		SELECT channel_id, id, previous_balance, balance, balance - previous_balance,
			CASE WHEN balance > previous_balance THEN 'recharge' ELSE 'consumption' END,
			sampled_at
		FROM (
			SELECT id, channel_id, balance, sampled_at,
				LAG(balance) OVER (PARTITION BY channel_id ORDER BY sampled_at ASC, id ASC) AS previous_balance
			FROM balance_snapshots
		) snapshots
		WHERE previous_balance IS NOT NULL AND ABS(balance - previous_balance) > 0.000000001
		ON CONFLICT (balance_snapshot_id) DO NOTHING
	`).Error; err != nil {
		return err
	}
	// 帮助文档数据迁移必须在表结构就绪后执行：绑定中转站、丢弃旧配置表、
	// 把遗留 Markdown 正文转换成富文本 HTML。
	return migrateHelpData(db)
}

// migrateGroupModelPricing upgrades legacy snapshots without resetting a saved toggle.
// PostgreSQL keeps the column addition and its one-time visibility backfill atomic.
func migrateGroupModelPricing(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if !tx.Migrator().HasTable(&RelayGroup{}) || tx.Migrator().HasColumn(&RelayGroup{}, "model_pricing_enabled") {
			return nil
		}
		if err := tx.Migrator().AddColumn(&RelayGroup{}, "ModelPricingEnabled"); err != nil {
			return err
		}
		backfill := "UPDATE relay_groups SET model_pricing_enabled = true"
		if tx.Migrator().HasColumn(&RelayGroup{}, "is_exclusive") {
			backfill = "UPDATE relay_groups SET model_pricing_enabled = NOT COALESCE(is_exclusive, false)"
		}
		return tx.Exec(backfill).Error
	})
}

// migrateDegradationEffortDefaults changes only the database defaults for new configs.
// Existing rows keep their explicitly stored effort values.
func migrateDegradationEffortDefaults(db *gorm.DB) error {
	for _, table := range []string{"group_degradation_configs", "account_degradation_configs"} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Exec(fmt.Sprintf("ALTER TABLE %s ALTER COLUMN effort SET DEFAULT 'none'", table)).Error; err != nil {
			return err
		}
	}
	return nil
}

// migrateDegradationMethodConstraint repairs the CHECK constraint shipped by the
// first release, which only accepted pelican/candy. GORM never rewrites an
// existing constraint, so a database created before the "鹈鹕和糖果" option kept
// rejecting that choice with a generic save failure.
func migrateDegradationMethodConstraint(db *gorm.DB) error {
	if !db.Migrator().HasTable("group_degradation_configs") {
		return nil
	}
	var row struct{ Definition string }
	// A missing constraint leaves the struct empty and is repaired below; an
	// already permissive definition is left untouched.
	if err := db.Raw(`SELECT COALESCE(pg_get_constraintdef(oid), '') AS definition FROM pg_constraint WHERE conname = 'chk_group_degradation_method' AND conrelid = 'group_degradation_configs'::regclass`).Scan(&row).Error; err != nil {
		return err
	}
	if strings.Contains(row.Definition, "'both'") {
		return nil
	}
	if err := db.Exec("ALTER TABLE group_degradation_configs DROP CONSTRAINT IF EXISTS chk_group_degradation_method").Error; err != nil {
		return err
	}
	return db.Exec("ALTER TABLE group_degradation_configs ADD CONSTRAINT chk_group_degradation_method CHECK (method IN ('pelican','candy','both'))").Error
}
