package storage

import (
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Channels 渠道仓库。
type Channels struct{ db *gorm.DB }

func NewChannels(db *gorm.DB) *Channels { return &Channels{db: db} }

func (r *Channels) Create(c *Channel) error { return r.db.Create(c).Error }
func (r *Channels) Update(c *Channel) error { return r.db.Save(c).Error }

func (r *Channels) ListAccounts(channelID uint) ([]ChannelAccount, error) {
	var list []ChannelAccount
	err := r.db.Where("channel_id = ?", channelID).
		Order("is_primary DESC").Order("id ASC").Find(&list).Error
	return list, err
}

func (r *Channels) ListAccountsForChannels(channelIDs []uint) (map[uint][]ChannelAccount, error) {
	result := make(map[uint][]ChannelAccount, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	var list []ChannelAccount
	if err := r.db.Where("channel_id IN ?", channelIDs).
		Order("channel_id ASC").Order("is_primary DESC").Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	for _, account := range list {
		result[account.ChannelID] = append(result[account.ChannelID], account)
	}
	return result, nil
}

// EnsurePrimaryAccount mirrors the legacy channel credential into the primary
// child record without overwriting that account's independently sampled
// balance. It makes legacy and newly-created channels share one account shape.
func (r *Channels) EnsurePrimaryAccount(c *Channel) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var primary ChannelAccount
		err := tx.Where("channel_id = ? AND is_primary = ?", c.ID, true).First(&primary).Error
		if err == gorm.ErrRecordNotFound {
			return tx.Create(&ChannelAccount{
				ChannelID: c.ID, IsPrimary: true,
				Username: c.Username, PasswordCipher: c.PasswordCipher,
				CredentialMode: c.CredentialMode, TurnstileEnabled: c.TurnstileEnabled,
				CaptchaConfigID: c.CaptchaConfigID, LastBalance: c.LastBalance,
				LastBalanceAt: c.LastBalanceAt, LastError: c.LastError,
			}).Error
		}
		if err != nil {
			return err
		}
		return tx.Model(&primary).Updates(map[string]any{
			"username": c.Username, "password_cipher": c.PasswordCipher,
			"credential_mode": c.CredentialMode, "turnstile_enabled": c.TurnstileEnabled,
			"captcha_config_id": c.CaptchaConfigID,
		}).Error
	})
}

// ReplaceAdditionalAccounts replaces only non-primary accounts for a channel.
// PasswordCipher values have already been validated and encrypted by the
// channel service. Session keys for removed or changed accounts are cleared in
// the same transaction.
func (r *Channels) ReplaceAdditionalAccounts(channelID uint, accounts []ChannelAccount, resetSessionIDs []uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing []ChannelAccount
		if err := tx.Where("channel_id = ? AND is_primary = ?", channelID, false).Find(&existing).Error; err != nil {
			return err
		}
		existingByID := make(map[uint]ChannelAccount, len(existing))
		for _, account := range existing {
			existingByID[account.ID] = account
		}
		kept := make(map[uint]struct{}, len(accounts))
		for _, account := range accounts {
			account.ChannelID = channelID
			account.IsPrimary = false
			if account.ID == 0 {
				if err := tx.Create(&account).Error; err != nil {
					return err
				}
				continue
			}
			if _, ok := existingByID[account.ID]; !ok {
				return gorm.ErrRecordNotFound
			}
			kept[account.ID] = struct{}{}
			if err := tx.Model(&ChannelAccount{}).Where("id = ? AND channel_id = ? AND is_primary = ?", account.ID, channelID, false).Updates(map[string]any{
				"username": account.Username, "password_cipher": account.PasswordCipher,
				"credential_mode": account.CredentialMode, "turnstile_enabled": account.TurnstileEnabled,
				"captcha_config_id": account.CaptchaConfigID, "last_error": account.LastError,
			}).Error; err != nil {
				return err
			}
		}

		removed := make([]uint, 0, len(existing))
		for _, account := range existing {
			if _, ok := kept[account.ID]; !ok {
				removed = append(removed, account.ID)
			}
		}
		if len(removed) > 0 {
			keys := accountSessionKeys(removed)
			if err := tx.Where("channel_id IN ?", keys).Delete(&AuthSession{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", removed).Delete(&ChannelAccount{}).Error; err != nil {
				return err
			}
		}
		if len(resetSessionIDs) > 0 {
			return tx.Where("channel_id IN ?", accountSessionKeys(resetSessionIDs)).Delete(&AuthSession{}).Error
		}
		return nil
	})
}

func (r *Channels) DeleteAccounts(channelID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var ids []uint
		if err := tx.Model(&ChannelAccount{}).Where("channel_id = ? AND is_primary = ?", channelID, false).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := tx.Where("channel_id IN ?", accountSessionKeys(ids)).Delete(&AuthSession{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("channel_id = ?", channelID).Delete(&ChannelAccount{}).Error
	})
}

func (r *Channels) UpdateAccountBalance(id uint, balance float64, at any, lastErr string) error {
	return r.db.Model(&ChannelAccount{}).Where("id = ?", id).Updates(map[string]any{
		"last_balance": balance, "last_balance_at": at, "last_error": lastErr,
	}).Error
}

func (r *Channels) SetAccountError(id uint, message string) error {
	return r.db.Model(&ChannelAccount{}).Where("id = ?", id).Update("last_error", message).Error
}

func accountSessionKeys(ids []uint) []uint {
	keys := make([]uint, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, AdditionalAccountSessionKey(id))
	}
	return keys
}

// ClearManualModeArtifacts removes data that could make a manually managed
// channel appear as an upstream group or an automatic cost source.
func (r *Channels) ClearManualModeArtifacts(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&RelayChannelMapping{}).Where("monitor_channel_id = ?", id).Updates(map[string]any{
			"monitor_channel_id": nil,
			"upstream_group":     "",
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("monitor_channel_id = ?", id).Delete(&RelayAccountCostOverride{}).Error; err != nil {
			return err
		}
		return nil
	})
}
func (r *Channels) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var accountIDs []uint
		if err := tx.Model(&ChannelAccount{}).Where("channel_id = ? AND is_primary = ?", id, false).Pluck("id", &accountIDs).Error; err != nil {
			return err
		}
		if len(accountIDs) > 0 {
			if err := tx.Where("channel_id IN ?", accountSessionKeys(accountIDs)).Delete(&AuthSession{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&OperationLedgerEntry{}).Where("channel_id = ?", id).Update("channel_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&RelayChannelMapping{}).Where("monitor_channel_id = ?", id).Updates(map[string]any{
			"monitor_channel_id": nil,
			"upstream_group":     "",
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("monitor_channel_id = ?", id).Delete(&RelayAccountCostOverride{}).Error; err != nil {
			return err
		}
		for _, target := range []any{
			&AuthSession{},
			&ChannelAccount{},
			&RateSnapshot{},
			&RateChangeLog{},
			&BalanceChangeLog{},
			&ChannelDailyBalance{},
			&BalanceSnapshot{},
			&NotificationLog{},
			&NotificationCooldown{},
			&MonitorLog{},
		} {
			if err := tx.Where("channel_id = ?", id).Delete(target).Error; err != nil {
				return err
			}
		}
		return tx.Unscoped().Delete(&Channel{}, id).Error
	})
}
func (r *Channels) FindByID(id uint) (*Channel, error) {
	var c Channel
	if err := r.db.First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Channels) FindByName(name string) (*Channel, error) {
	var c Channel
	if err := r.db.Where("name = ?", name).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *Channels) List() ([]Channel, error) {
	var list []Channel
	if err := r.db.Order("id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListForManagement 按最近一次实际分组倍率变动倒序返回渠道；从未发生过变动的渠道排在后面。
func (r *Channels) ListForManagement() ([]Channel, error) {
	var list []Channel
	err := r.db.Model(&Channel{}).
		Select("channels.*, rate_changes.latest_ratio_changed_at").
		Joins(`LEFT JOIN (
			SELECT changes.channel_id, MAX(changes.changed_at) AS latest_ratio_changed_at
			FROM rate_change_logs AS changes
			JOIN rate_snapshots AS snapshots
				ON snapshots.channel_id = changes.channel_id
				AND snapshots.model_name = changes.model_name
			WHERE changes.old_ratio IS NOT NULL
				AND changes.old_ratio <> changes.new_ratio
				AND ABS(changes.new_ratio - snapshots.ratio) <= 0.000000001
			GROUP BY changes.channel_id
		) AS rate_changes ON rate_changes.channel_id = channels.id`).
		Order("rate_changes.latest_ratio_changed_at DESC NULLS LAST").
		Order("channels.id ASC").
		Find(&list).Error
	return list, err
}
func (r *Channels) ListMonitorEnabled() ([]Channel, error) {
	var list []Channel
	if err := r.db.Where("monitor_enabled = ?", true).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}
func (r *Channels) UpdateBalance(id uint, balance float64, at any, lastErr string) error {
	return r.db.Model(&Channel{}).Where("id = ?", id).Updates(map[string]any{
		"last_balance":    balance,
		"last_balance_at": at,
		"last_error":      lastErr,
	}).Error
}

// UpdateManualBalance persists the displayed balance and the cumulative relay
// cost it was settled against in one write, so a later refresh only deducts a
// newly observed increment.
func (r *Channels) UpdateManualBalance(id uint, balance float64, at any, lastErr string, usageBaseline *float64, usageBasis string) error {
	_, err := r.SettleManualBalance(id, balance, balance, at, lastErr, usageBaseline, usageBasis)
	return err
}

// SettleManualBalance persists a usage settlement while preserving a recharge
// or explicit balance edit that happened after the monitor read the channel.
// observedBalance is the value used to calculate settledBalance; the locked
// row delta is carried forward into the final displayed balance.
func (r *Channels) SettleManualBalance(id uint, observedBalance, settledBalance float64, at any, lastErr string, usageBaseline *float64, usageBasis string) (float64, error) {
	finalBalance := settledBalance
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current Channel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, id).Error; err != nil {
			return err
		}
		settledBalance = carryForwardManualBalanceChange(observedBalance, settledBalance, current.LastBalance)
		finalBalance = settledBalance
		return tx.Model(&current).Updates(map[string]any{
			"last_balance":          settledBalance,
			"last_balance_at":       at,
			"last_error":            lastErr,
			"manual_usage_baseline": usageBaseline,
			"manual_usage_basis":    usageBasis,
		}).Error
	})
	return finalBalance, err
}

func carryForwardManualBalanceChange(observedBalance, settledBalance float64, currentBalance *float64) float64 {
	if currentBalance != nil && math.Abs(*currentBalance-observedBalance) > 0.000000001 {
		settledBalance += *currentBalance - observedBalance
	}
	if settledBalance < 0 {
		return 0
	}
	return settledBalance
}

func (r *Channels) SetLastError(id uint, msg string) error {
	return r.db.Model(&Channel{}).Where("id = ?", id).Update("last_error", msg).Error
}
