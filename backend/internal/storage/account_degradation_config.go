package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidAccountDegradationConfig = errors.New("账号降智检测配置无效")

type AccountDegradationConfig struct {
	ID                uint       `gorm:"primaryKey" json:"-"`
	StationID         uint       `gorm:"not null;uniqueIndex:idx_account_degradation_config_scope" json:"-"`
	AccountExternalID int64      `gorm:"not null;uniqueIndex:idx_account_degradation_config_scope" json:"-"`
	Enabled           bool       `gorm:"not null;default:false;index:idx_account_degradation_config_due,priority:1" json:"enabled"`
	Method            string     `gorm:"size:16;not null;default:pelican" json:"method"`
	Model             string     `gorm:"size:256;not null;default:''" json:"model"`
	JudgeModel        string     `gorm:"size:256;not null;default:''" json:"judge_model"`
	Effort            string     `gorm:"size:16;not null;default:none" json:"effort"`
	PelicanInterval   int        `gorm:"not null;default:60" json:"pelican_interval_minutes"`
	CandyInterval     int        `gorm:"not null;default:10" json:"candy_interval_minutes"`
	HistoryLimit      int        `gorm:"not null;default:20" json:"history_limit"`
	DisableOnDegraded bool       `gorm:"not null;default:false" json:"disable_on_degraded"`
	Protocol          string     `gorm:"size:16;not null;default:''" json:"protocol"`
	RunMethod         string     `gorm:"-" json:"-"`
	PelicanNextRunAt  *time.Time `json:"-"`
	CandyNextRunAt    *time.Time `json:"-"`
	LeaseToken        string     `gorm:"size:64;not null;default:''" json:"-"`
	LeaseUntil        *time.Time `json:"-"`
	CreatedAt         time.Time  `json:"-"`
	UpdatedAt         time.Time  `json:"-"`
}

func (AccountDegradationConfig) TableName() string { return "account_degradation_configs" }

func DefaultAccountDegradationConfig() AccountDegradationConfig {
	return AccountDegradationConfig{Method: "pelican", Effort: "none", PelicanInterval: 60, CandyInterval: 10, HistoryLimit: DefaultGroupDegradationHistoryLimit}
}

func NormalizeAccountDegradationConfig(c AccountDegradationConfig) (AccountDegradationConfig, error) {
	c.Method, c.Model, c.JudgeModel, c.Effort, c.Protocol = strings.TrimSpace(c.Method), strings.TrimSpace(c.Model), strings.TrimSpace(c.JudgeModel), strings.TrimSpace(c.Effort), strings.TrimSpace(c.Protocol)
	if c.Method == "" {
		c.Method = "pelican"
	}
	if c.Effort == "" {
		c.Effort = "none"
	}
	if c.PelicanInterval == 0 {
		c.PelicanInterval = 60
	}
	if c.CandyInterval == 0 {
		c.CandyInterval = 10
	}
	if c.HistoryLimit == 0 {
		c.HistoryLimit = DefaultGroupDegradationHistoryLimit
	}
	invalid := func(reason string) (AccountDegradationConfig, error) {
		return c, fmt.Errorf("%w：%s", ErrInvalidAccountDegradationConfig, reason)
	}
	if c.Method != "pelican" && c.Method != "candy" && c.Method != "both" {
		return invalid("method 必须是 pelican、candy 或 both")
	}
	if c.Effort != "none" && c.Effort != "low" && c.Effort != "medium" && c.Effort != "high" {
		return invalid("effort 无效")
	}
	if c.PelicanInterval < 5 || c.PelicanInterval > 1440 || c.CandyInterval < 5 || c.CandyInterval > 1440 {
		return invalid("检测间隔必须在 5 到 1440 分钟之间")
	}
	if c.HistoryLimit < 1 || c.HistoryLimit > MaxGroupDegradationHistoryLimit {
		return invalid("历史最多保留 1 到 1000 条")
	}
	if c.Enabled && c.Model == "" {
		return invalid("启用时必须选择调用模型")
	}
	for _, model := range []string{c.Model, c.JudgeModel} {
		if len(model) > 256 {
			return invalid("模型名称过长")
		}
	}
	return c, nil
}

func (r *RelayStations) AccountDegradationConfig(ctx context.Context, stationID uint, accountID int64) (AccountDegradationConfig, error) {
	config := DefaultAccountDegradationConfig()
	if err := r.accountDegradationScope(ctx, stationID, accountID); err != nil {
		return config, err
	}
	err := r.db.WithContext(ctx).Where("station_id = ? AND account_external_id = ?", stationID, accountID).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DefaultAccountDegradationConfig(), nil
	}
	return config, err
}

func (r *RelayStations) SaveAccountDegradationConfig(ctx context.Context, stationID uint, accountID int64, config AccountDegradationConfig) (AccountDegradationConfig, error) {
	config, err := NormalizeAccountDegradationConfig(config)
	if err != nil {
		return config, err
	}
	if err := r.accountDegradationScope(ctx, stationID, accountID); err != nil {
		return config, err
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := DefaultAccountDegradationConfig()
		seed.StationID, seed.AccountExternalID = stationID, accountID
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "station_id"}, {Name: "account_external_id"}}, DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		var current AccountDegradationConfig
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND account_external_id = ?", stationID, accountID).First(&current).Error; err != nil {
			return err
		}
		updates := map[string]any{"enabled": config.Enabled, "method": config.Method, "model": config.Model, "judge_model": config.JudgeModel, "effort": config.Effort, "pelican_interval": config.PelicanInterval, "candy_interval": config.CandyInterval, "history_limit": config.HistoryLimit, "disable_on_degraded": config.DisableOnDegraded, "protocol": config.Protocol, "lease_token": "", "lease_until": nil, "pelican_next_run_at": nil, "candy_next_run_at": nil}
		if config.Enabled {
			now := time.Now()
			if config.Method == "pelican" || config.Method == "both" {
				updates["pelican_next_run_at"] = now.Add(time.Duration(config.PelicanInterval) * time.Minute)
			}
			if config.Method == "candy" || config.Method == "both" {
				updates["candy_next_run_at"] = now.Add(time.Duration(config.CandyInterval) * time.Minute)
			}
		}
		if err := tx.Model(&current).Updates(updates).Error; err != nil {
			return err
		}
		return pruneAccountDegradationHistory(tx, stationID, accountID, config.HistoryLimit)
	})
	return config, err
}

func pruneAccountDegradationHistory(tx *gorm.DB, stationID uint, accountID int64, limit int) error {
	stale := tx.Model(&AccountDegradationResult{}).Select("id").Where("station_id = ? AND account_external_id = ?", stationID, accountID).Order("created_at DESC").Order("id DESC").Offset(limit)
	return tx.Where("station_id = ? AND account_external_id = ?", stationID, accountID).Where("id IN (?)", stale).Delete(&AccountDegradationResult{}).Error
}

func (r *RelayStations) ClaimDueAccountDegradation(ctx context.Context, limit int, lease time.Duration) ([]AccountDegradationConfig, error) {
	configs := []AccountDegradationConfig{}
	if limit <= 0 {
		return configs, nil
	}
	if limit > 4 {
		limit = 4
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("enabled = ? AND (lease_until IS NULL OR lease_until <= ?)", true, now).Where("(pelican_next_run_at IS NOT NULL AND pelican_next_run_at <= ?) OR (candy_next_run_at IS NOT NULL AND candy_next_run_at <= ?)", now, now).Order("id ASC").Limit(limit).Find(&configs).Error; err != nil {
			return err
		}
		for i := range configs {
			method := accountDegradationDueMethod(configs[i], now)
			if method == "" {
				continue
			}
			until := now.Add(9 * time.Minute)
			token, err := newGroupDegradationLease()
			if err != nil {
				return err
			}
			p := accountDegradationAdvance(configs[i].PelicanNextRunAt, now, configs[i].PelicanInterval, method == "pelican")
			c := accountDegradationAdvance(configs[i].CandyNextRunAt, now, configs[i].CandyInterval, method == "candy")
			if err := tx.Model(&configs[i]).Updates(map[string]any{"lease_token": token, "lease_until": until, "pelican_next_run_at": p, "candy_next_run_at": c}).Error; err != nil {
				return err
			}
			configs[i].LeaseToken, configs[i].LeaseUntil, configs[i].RunMethod = token, &until, method
		}
		return nil
	})
	return configs, err
}

func accountDegradationDueMethod(c AccountDegradationConfig, now time.Time) string {
	if (c.Method == "pelican" || c.Method == "both") && c.PelicanNextRunAt != nil && !c.PelicanNextRunAt.After(now) {
		return "pelican"
	}
	if (c.Method == "candy" || c.Method == "both") && c.CandyNextRunAt != nil && !c.CandyNextRunAt.After(now) {
		return "candy"
	}
	return ""
}

func accountDegradationAdvance(previous *time.Time, now time.Time, interval int, due bool) *time.Time {
	if previous == nil || !due {
		return previous
	}
	step := time.Duration(interval) * time.Minute
	next := previous.Add(step)
	for !next.After(now) {
		next = next.Add(step)
	}
	return &next
}

func (r *RelayStations) CompleteDueAccountDegradation(ctx context.Context, claimed AccountDegradationConfig, limit int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current AccountDegradationConfig
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND account_external_id = ?", claimed.StationID, claimed.AccountExternalID).First(&current, claimed.ID).Error; err != nil {
			return err
		}
		if current.LeaseToken != claimed.LeaseToken || current.LeaseToken == "" {
			return ErrGroupDegradationLeaseLost
		}
		if err := pruneAccountDegradationHistory(tx, current.StationID, current.AccountExternalID, limit); err != nil {
			return err
		}
		return tx.Model(&current).Updates(map[string]any{"lease_token": "", "lease_until": nil}).Error
	})
}
