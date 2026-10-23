package storage

import (
	"context"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Manual account tests intentionally have no enabled flag or schedule.
type AccountDegradationRun struct {
	ID                uint   `gorm:"primaryKey"`
	StationID         uint   `gorm:"not null;uniqueIndex:idx_account_degradation_scope"`
	AccountExternalID int64  `gorm:"not null;uniqueIndex:idx_account_degradation_scope"`
	LeaseToken        string `gorm:"size:64;not null;default:''"`
	LeaseUntil        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (AccountDegradationRun) TableName() string { return "account_degradation_runs" }

type AccountDegradationResult struct {
	GroupDegradationResult `gorm:"embedded"`
	AccountExternalID      int64     `gorm:"not null;index:idx_account_degradation_results,priority:2" json:"-"`
	StationID              uint      `gorm:"not null;index:idx_account_degradation_results,priority:1" json:"-"`
	CreatedAt              time.Time `gorm:"not null;index:idx_account_degradation_results,priority:3,sort:desc" json:"created_at"`
	GroupExternalID        int64     `gorm:"not null;default:0" json:"-"`
}

func (AccountDegradationResult) TableName() string { return "account_degradation_results" }

func (r *RelayStations) accountDegradationScope(ctx context.Context, stationID uint, accountID int64) error {
	var station RelayStation
	if err := r.db.WithContext(ctx).Select("id").First(&station, stationID).Error; err != nil {
		return err
	}
	var account RelayAccount
	return r.db.WithContext(ctx).Select("id").Where("relay_station_id = ? AND external_id = ?", stationID, accountID).First(&account).Error
}

func (r *RelayStations) AccountDegradationHistory(ctx context.Context, stationID uint, accountID int64) ([]AccountDegradationResult, error) {
	results := []AccountDegradationResult{}
	if err := r.accountDegradationScope(ctx, stationID, accountID); err != nil {
		return results, err
	}
	err := r.db.WithContext(ctx).Where("station_id = ? AND account_external_id = ?", stationID, accountID).
		Order("created_at DESC").Order("id DESC").Limit(DefaultGroupDegradationHistoryLimit).Find(&results).Error
	return results, err
}

// LatestAccountDegradation uses one result query for the entire station.
func (r *RelayStations) LatestAccountDegradation(ctx context.Context, stationID uint) (map[string]AccountDegradationResult, error) {
	latest := map[string]AccountDegradationResult{}
	var station RelayStation
	if err := r.db.WithContext(ctx).Select("id").First(&station, stationID).Error; err != nil {
		return latest, err
	}
	var results []AccountDegradationResult
	err := r.db.WithContext(ctx).Raw(`
		SELECT DISTINCT ON (r.account_external_id) r.*
		FROM account_degradation_results r
		JOIN relay_accounts a ON a.relay_station_id = r.station_id AND a.external_id = r.account_external_id
		WHERE r.station_id = ?
		ORDER BY r.account_external_id, r.created_at DESC, r.id DESC`, stationID).Scan(&results).Error
	if err != nil {
		return latest, err
	}
	for _, result := range results {
		latest[strconv.FormatInt(result.AccountExternalID, 10)] = result
	}
	return latest, nil
}

func (r *RelayStations) ClaimAccountDegradation(ctx context.Context, stationID uint, accountID int64, lease time.Duration) (AccountDegradationRun, error) {
	var run AccountDegradationRun
	if err := r.accountDegradationScope(ctx, stationID, accountID); err != nil {
		return run, err
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		seed := AccountDegradationRun{StationID: stationID, AccountExternalID: accountID}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "station_id"}, {Name: "account_external_id"}}, DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND account_external_id = ?", stationID, accountID).First(&run).Error; err != nil {
			return err
		}
		now := time.Now()
		if run.LeaseUntil != nil && run.LeaseUntil.After(now) {
			return ErrGroupDegradationBusy
		}
		token, err := newGroupDegradationLease()
		if err != nil {
			return err
		}
		until := now.Add(lease)
		if err := tx.Model(&run).Updates(map[string]any{"lease_token": token, "lease_until": until}).Error; err != nil {
			return err
		}
		run.LeaseToken, run.LeaseUntil = token, &until
		return nil
	})
	return run, err
}

func (r *RelayStations) CompleteAccountDegradation(ctx context.Context, claimed AccountDegradationRun, result *AccountDegradationResult) error {
	return r.CompleteAccountDegradationWithLimit(ctx, claimed, result, DefaultGroupDegradationHistoryLimit)
}

func (r *RelayStations) CompleteAccountDegradationWithLimit(ctx context.Context, claimed AccountDegradationRun, result *AccountDegradationResult, historyLimit int) error {
	if claimed.ID == 0 || claimed.StationID == 0 || claimed.AccountExternalID <= 0 || claimed.LeaseToken == "" || result == nil {
		return ErrGroupDegradationLeaseLost
	}
	if historyLimit < 1 || historyLimit > MaxGroupDegradationHistoryLimit {
		return ErrInvalidAccountDegradationConfig
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current AccountDegradationRun
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("station_id = ? AND account_external_id = ?", claimed.StationID, claimed.AccountExternalID).First(&current, claimed.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrGroupDegradationLeaseLost
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if current.LeaseToken != claimed.LeaseToken || current.LeaseUntil == nil || !current.LeaseUntil.After(now) {
			return ErrGroupDegradationLeaseLost
		}
		result.ID = 0
		result.StationID, result.AccountExternalID, result.GroupExternalID, result.CreatedAt = current.StationID, current.AccountExternalID, 0, now
		if err := tx.Create(result).Error; err != nil {
			return err
		}
		if err := pruneAccountDegradationHistory(tx, current.StationID, current.AccountExternalID, historyLimit); err != nil {
			return err
		}
		return tx.Model(&current).Updates(map[string]any{"lease_token": "", "lease_until": nil}).Error
	})
}

// ClearAccountDegradationHistory removes one account's manual test records. The
// account keeps its configuration and scheduling: a run in flight holds its
// lease, so clearing waits until it finishes instead of dropping its result.
func (r *RelayStations) ClearAccountDegradationHistory(ctx context.Context, stationID uint, accountID int64) error {
	if err := r.accountDegradationScope(ctx, stationID, accountID); err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run AccountDegradationRun
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("station_id = ? AND account_external_id = ?", stationID, accountID).First(&run).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && run.LeaseUntil != nil && run.LeaseUntil.After(time.Now()) {
			return ErrGroupDegradationBusy
		}
		return tx.Where("station_id = ? AND account_external_id = ?", stationID, accountID).
			Delete(&AccountDegradationResult{}).Error
	})
}
