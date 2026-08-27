package storage

import (
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Rates struct{ db *gorm.DB }

func NewRates(db *gorm.DB) *Rates { return &Rates{db: db} }

const (
	validBalanceEpsilon    = 0.000000001
	transientResetWindow   = 30 * time.Minute
	transientRecoveryRatio = 0.90
)

var ErrRelayAccountRateReadOnly = errors.New("自动关联账号分组由中转站同步，不能手动修改")

// ListByChannel 返回渠道当前所有倍率快照。
func (r *Rates) ListByChannel(channelID uint) ([]RateSnapshot, error) {
	var list []RateSnapshot
	if err := r.db.Where("channel_id = ?", channelID).Order("model_name ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *Rates) FindByID(id uint, channelID uint) (*RateSnapshot, error) {
	var snapshot RateSnapshot
	if err := r.db.Where("id = ? AND channel_id = ?", id, channelID).First(&snapshot).Error; err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// Upsert atomically updates the current snapshot and records a real ratio
// change. Returning the previous row lets the caller build notifications from
// the same transition that was committed to history.
func (r *Rates) Upsert(snapshot *RateSnapshot) (*RateSnapshot, error) {
	var previous *RateSnapshot
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current RateSnapshot
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("channel_id = ? AND model_name = ?", snapshot.ChannelID, snapshot.ModelName).
			First(&current).Error
		switch {
		case err == gorm.ErrRecordNotFound:
			snapshot.FirstSeenAt = snapshot.LastSeenAt
			return tx.Create(snapshot).Error
		case err != nil:
			return err
		}

		// An older overlapping scan must not overwrite a newer observation.
		if !snapshot.LastSeenAt.IsZero() && snapshot.LastSeenAt.Before(current.LastSeenAt) {
			return nil
		}

		old := current
		current.Ratio = snapshot.Ratio
		current.CompletionRatio = snapshot.CompletionRatio
		current.Description = snapshot.Description
		if snapshot.Source != "" {
			current.Source = snapshot.Source
			current.RelayStationID = snapshot.RelayStationID
			current.RelayAccountExternalID = snapshot.RelayAccountExternalID
		}
		current.LastSeenAt = snapshot.LastSeenAt
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		if old.Ratio != snapshot.Ratio || old.CompletionRatio != snapshot.CompletionRatio {
			oldRatio, oldCompletion := old.Ratio, old.CompletionRatio
			changedAt := snapshot.LastSeenAt
			if changedAt.IsZero() {
				changedAt = time.Now()
			}
			if err := tx.Create(&RateChangeLog{
				ChannelID: snapshot.ChannelID, ModelName: snapshot.ModelName,
				OldRatio: &oldRatio, NewRatio: snapshot.Ratio,
				OldCompletionRatio: &oldCompletion, NewCompletionRatio: snapshot.CompletionRatio,
				ChangedAt: changedAt,
			}).Error; err != nil {
				return err
			}
		}
		previous = &old
		return nil
	})
	return previous, err
}

// Delete removes one current group snapshot. Historical change logs are kept.
func (r *Rates) Delete(id uint, channelID uint) error {
	return r.db.Where("id = ? AND channel_id = ?", id, channelID).Delete(&RateSnapshot{}).Error
}

// SaveManual stores a user-managed group while preserving its stable ID.
func (r *Rates) SaveManual(snapshot *RateSnapshot) error {
	var current RateSnapshot
	if snapshot.ID != 0 {
		if err := r.db.Where("id = ? AND channel_id = ?", snapshot.ID, snapshot.ChannelID).First(&current).Error; err != nil {
			return err
		}
		if current.Source == RateSnapshotSourceRelayAccount {
			return ErrRelayAccountRateReadOnly
		}
	}
	var duplicate RateSnapshot
	query := r.db.Where("channel_id = ? AND model_name = ?", snapshot.ChannelID, snapshot.ModelName)
	if snapshot.ID != 0 {
		query = query.Where("id <> ?", snapshot.ID)
	}
	if err := query.First(&duplicate).Error; err == nil {
		return gorm.ErrDuplicatedKey
	} else if err != gorm.ErrRecordNotFound {
		return err
	}
	if snapshot.FirstSeenAt.IsZero() {
		snapshot.FirstSeenAt = time.Now()
	}
	if snapshot.LastSeenAt.IsZero() {
		snapshot.LastSeenAt = snapshot.FirstSeenAt
	}
	snapshot.Source = RateSnapshotSourceManual
	snapshot.RelayStationID = nil
	snapshot.RelayAccountExternalID = nil
	if snapshot.ID == 0 {
		return r.db.Create(snapshot).Error
	}
	return r.db.Model(&current).Updates(map[string]any{
		"model_name": snapshot.ModelName, "description": snapshot.Description,
		"ratio": snapshot.Ratio, "completion_ratio": snapshot.CompletionRatio,
		"source": RateSnapshotSourceManual, "relay_station_id": nil, "relay_account_external_id": nil,
		"last_seen_at": snapshot.LastSeenAt,
	}).Error
}

// DeleteMissing removes snapshots that were not returned by the latest
// successful upstream scan. RateChangeLog is intentionally left untouched so
// historical changes remain available after a group is removed upstream.
func (r *Rates) DeleteMissing(channelID uint, modelNames []string) (int64, error) {
	query := r.db.Where("channel_id = ?", channelID)
	if len(modelNames) > 0 {
		query = query.Where("model_name NOT IN ?", modelNames)
	}
	result := query.Delete(&RateSnapshot{})
	return result.RowsAffected, result.Error
}

func (r *Rates) AppendChange(log *RateChangeLog) error {
	if log.ChangedAt.IsZero() {
		log.ChangedAt = time.Now()
	}
	return r.db.Create(log).Error
}

// ListChanges 倒序拉取倍率变化日志。channelID 为 0 表示不过滤；ratioOnly 排除仅补全倍率变化的记录。
func (r *Rates) ListChanges(channelID uint, limit int, ratioOnly bool) ([]RateChangeLog, error) {
	if limit <= 0 {
		limit = 50
	}
	q := r.db.Model(&RateChangeLog{}).Order("changed_at DESC").Limit(limit)
	if channelID != 0 {
		q = q.Where("channel_id = ?", channelID)
	}
	if ratioOnly {
		q = q.Where("old_ratio IS NOT NULL AND old_ratio <> new_ratio")
	}
	var list []RateChangeLog
	if err := q.Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *Rates) ListChangesSince(since time.Time, limit int) ([]RateChangeLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []RateChangeLog
	err := r.db.Where("changed_at >= ? AND old_ratio IS NOT NULL AND old_ratio <> new_ratio", since).
		Order("changed_at DESC").Limit(limit).Find(&list).Error
	return list, err
}

func (r *Rates) CountChangesSince(since time.Time) (int64, error) {
	var count int64
	err := r.db.Model(&RateChangeLog{}).
		Where("changed_at >= ? AND old_ratio IS NOT NULL AND old_ratio <> new_ratio", since).
		Count(&count).Error
	return count, err
}

// ListLatestRatioChanges returns the latest transition into each current
// group ratio. Historical transitions whose target no longer matches the
// current snapshot must not be presented as the group's latest state.
func (r *Rates) ListLatestRatioChanges(channelID uint) ([]RateChangeLog, error) {
	query := `
		SELECT DISTINCT ON (snapshots.channel_id, snapshots.model_name) changes.*
		FROM rate_snapshots AS snapshots
		JOIN rate_change_logs AS changes
			ON changes.channel_id = snapshots.channel_id
			AND changes.model_name = snapshots.model_name
		WHERE changes.old_ratio IS NOT NULL
			AND changes.old_ratio <> changes.new_ratio
			AND ABS(changes.new_ratio - snapshots.ratio) <= 0.000000001
	`
	args := make([]any, 0, 1)
	if channelID != 0 {
		query += " AND snapshots.channel_id = ?"
		args = append(args, channelID)
	}
	query += " ORDER BY snapshots.channel_id ASC, snapshots.model_name ASC, changes.changed_at DESC, changes.id DESC"

	var list []RateChangeLog
	if err := r.db.Raw(query, args...).Scan(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

func (r *Rates) AppendBalance(s *BalanceSnapshot) error {
	if s.SampledAt.IsZero() {
		s.SampledAt = time.Now()
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var previous BalanceSnapshot
		previousErr := tx.Where("channel_id = ? AND sampled_at <= ?", s.ChannelID, s.SampledAt).
			Order("sampled_at DESC, id DESC").First(&previous).Error
		if previousErr != nil && previousErr != gorm.ErrRecordNotFound {
			return previousErr
		}
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		if previousErr == gorm.ErrRecordNotFound {
			return r.ensureDailyBalance(tx, s)
		}
		delta := s.Balance - previous.Balance
		if math.Abs(delta) <= 0.000000001 {
			return r.ensureDailyBalance(tx, s)
		}
		kind := "consumption"
		if delta > 0 {
			kind = "recharge"
		}
		if err := tx.Create(&BalanceChangeLog{
			ChannelID: s.ChannelID, BalanceSnapshotID: s.ID,
			PreviousBalance: previous.Balance, NewBalance: s.Balance,
			Delta: delta, Kind: kind, DetectedAt: s.SampledAt,
		}).Error; err != nil {
			return err
		}
		return r.ensureDailyBalance(tx, s)
	})
}

// CaptureDailyBalances stores one balance for every channel on the local day
// containing at. The last known balance before midnight is preferred. If that
// value was a transient zero, the first valid positive balance observed during
// the day is used instead. A unique (channel_id, day) key makes both the
// midnight job and startup recovery safe to run repeatedly.
func (r *Rates) CaptureDailyBalances(at time.Time) (int64, error) {
	if at.IsZero() {
		at = time.Now()
	}
	dayStart, dayEnd := localDayBounds(at)
	day := dayStart.Format("2006-01-02")
	var channels []Channel
	if err := r.db.Where("deleted_at IS NULL").Order("id ASC").Find(&channels).Error; err != nil {
		return 0, err
	}

	var captured int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for _, channel := range channels {
			balance, err := dailyBalanceCandidate(tx, channel, dayStart, dayEnd)
			if err != nil {
				return err
			}
			if balance == nil || *balance <= validBalanceEpsilon {
				continue
			}
			result := tx.Exec(`
				INSERT INTO channel_daily_balances (channel_id, day, balance, captured_at)
				VALUES (?, CAST(? AS date), ?, ?)
				ON CONFLICT (channel_id, day) DO UPDATE
				SET balance = EXCLUDED.balance, captured_at = EXCLUDED.captured_at
				WHERE channel_daily_balances.balance <= ?
			`, channel.ID, day, *balance, at, validBalanceEpsilon)
			if result.Error != nil {
				return result.Error
			}
			captured += result.RowsAffected
		}
		return nil
	})
	return captured, err
}

// ChannelConsumptionTotals returns balance decreases grouped by channel. A
// zero balance followed by a near-complete recovery within a short window is
// treated as an upstream sampling reset, not a real consumption event. When a
// reliable range start and end balance are available, the result is capped at
// the net balance decrease so a reset/recovery pair cannot be double-counted.
func (r *Rates) ChannelConsumptionTotals(since time.Time) (map[uint]float64, error) {
	query := r.db.Model(&BalanceChangeLog{})
	if !since.IsZero() {
		query = query.Where("detected_at >= ?", since)
	}
	var changes []BalanceChangeLog
	if err := query.Order("channel_id ASC, detected_at ASC, id ASC").Find(&changes).Error; err != nil {
		return nil, err
	}
	totals := make(map[uint]float64)
	for i, change := range changes {
		if change.Delta >= 0 {
			continue
		}
		if i+1 < len(changes) && changes[i+1].ChannelID == change.ChannelID && isTransientBalanceReset(change, changes[i+1]) {
			continue
		}
		totals[change.ChannelID] += -change.Delta
	}
	if since.IsZero() {
		return totals, nil
	}
	bounds, err := r.rangeBalanceBounds(since)
	if err != nil {
		return nil, err
	}
	for channelID, amount := range totals {
		if bound, ok := bounds[channelID]; ok {
			totals[channelID] = capConsumptionToNet(amount, bound.Start, bound.End)
		}
	}
	return totals, nil
}

type balanceRangeBound struct {
	Start *float64
	End   *float64
}

func (r *Rates) rangeBalanceBounds(since time.Time) (map[uint]balanceRangeBound, error) {
	localSince := since.In(time.Local)
	day := localSince.Format("2006-01-02")
	type row struct {
		ChannelID uint
		Start     *float64
		End       *float64
	}
	var rows []row
	err := r.db.Raw(`
		SELECT c.id AS channel_id,
			COALESCE(
				NULLIF((SELECT d.balance
					FROM channel_daily_balances d
					WHERE d.channel_id = c.id AND d.day = CAST(? AS date)
					LIMIT 1), 0),
				NULLIF((SELECT b.balance
					FROM balance_snapshots b
					WHERE b.channel_id = c.id AND b.sampled_at < ?
					ORDER BY b.sampled_at DESC, b.id DESC
					LIMIT 1), 0),
				(SELECT b.balance
					FROM balance_snapshots b
					WHERE b.channel_id = c.id AND b.sampled_at >= ? AND b.balance > ?
					ORDER BY b.sampled_at ASC, b.id ASC
					LIMIT 1)
			) AS start,
			COALESCE(
				NULLIF(c.last_balance, 0),
				(SELECT b.balance
					FROM balance_snapshots b
					WHERE b.channel_id = c.id AND b.balance > ?
					ORDER BY b.sampled_at DESC, b.id DESC
					LIMIT 1)
			) AS "end"
		FROM channels c
		WHERE c.deleted_at IS NULL
	`, day, since, since, validBalanceEpsilon, validBalanceEpsilon).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	bounds := make(map[uint]balanceRangeBound, len(rows))
	for _, row := range rows {
		bounds[row.ChannelID] = balanceRangeBound{Start: row.Start, End: row.End}
	}
	return bounds, nil
}

func dailyBalanceCandidate(tx *gorm.DB, channel Channel, dayStart, dayEnd time.Time) (*float64, error) {
	var previous BalanceSnapshot
	previousErr := tx.Where("channel_id = ? AND sampled_at < ?", channel.ID, dayStart).
		Order("sampled_at DESC, id DESC").First(&previous).Error
	if previousErr != nil && previousErr != gorm.ErrRecordNotFound {
		return nil, previousErr
	}
	if previousErr == nil && previous.Balance > validBalanceEpsilon {
		return &previous.Balance, nil
	}
	var first BalanceSnapshot
	firstErr := tx.Where("channel_id = ? AND sampled_at >= ? AND sampled_at < ? AND balance > ?", channel.ID, dayStart, dayEnd, validBalanceEpsilon).
		Order("sampled_at ASC, id ASC").First(&first).Error
	if firstErr == nil {
		return &first.Balance, nil
	}
	if firstErr != gorm.ErrRecordNotFound {
		return nil, firstErr
	}
	if channel.LastBalance != nil && *channel.LastBalance > validBalanceEpsilon {
		return channel.LastBalance, nil
	}
	return nil, nil
}

func (r *Rates) ensureDailyBalance(tx *gorm.DB, snapshot *BalanceSnapshot) error {
	if snapshot.Balance <= validBalanceEpsilon {
		return nil
	}
	dayStart, _ := localDayBounds(snapshot.SampledAt)
	day := dayStart.Format("2006-01-02")
	var previous BalanceSnapshot
	previousErr := tx.Where("channel_id = ? AND sampled_at < ?", snapshot.ChannelID, dayStart).
		Order("sampled_at DESC, id DESC").First(&previous).Error
	initial := snapshot.Balance
	if previousErr == nil && previous.Balance > validBalanceEpsilon {
		initial = previous.Balance
	} else if previousErr != nil && previousErr != gorm.ErrRecordNotFound {
		return previousErr
	}
	return tx.Exec(`
		INSERT INTO channel_daily_balances (channel_id, day, balance, captured_at)
		VALUES (?, CAST(? AS date), ?, ?)
		ON CONFLICT (channel_id, day) DO UPDATE
		SET balance = EXCLUDED.balance, captured_at = EXCLUDED.captured_at
		WHERE channel_daily_balances.balance <= ?
	`, snapshot.ChannelID, day, initial, snapshot.SampledAt, validBalanceEpsilon).Error
}

func localDayBounds(at time.Time) (time.Time, time.Time) {
	local := at.In(time.Local)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
	return start, start.AddDate(0, 0, 1)
}

func isTransientBalanceReset(change, recovery BalanceChangeLog) bool {
	if change.Delta >= 0 || change.PreviousBalance <= validBalanceEpsilon || change.NewBalance > validBalanceEpsilon {
		return false
	}
	if recovery.ChannelID != change.ChannelID || recovery.Delta <= 0 || recovery.PreviousBalance > validBalanceEpsilon {
		return false
	}
	if recovery.DetectedAt.Before(change.DetectedAt) || recovery.DetectedAt.Sub(change.DetectedAt) > transientResetWindow {
		return false
	}
	return recovery.NewBalance >= change.PreviousBalance*transientRecoveryRatio
}

func capConsumptionToNet(amount float64, start, end *float64) float64 {
	if amount <= 0 || start == nil || end == nil || *start <= *end {
		if amount < 0 {
			return 0
		}
		return amount
	}
	net := *start - *end
	if amount > net {
		return net
	}
	return amount
}

// DeleteBalanceSnapshotsBefore 删除 sampled_at < cutoff 的余额快照，返回删除行数。
func (r *Rates) DeleteBalanceSnapshotsBefore(cutoff time.Time) (int64, error) {
	var deleted int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("detected_at < ?", cutoff).Delete(&BalanceChangeLog{}).Error; err != nil {
			return err
		}
		res := tx.Where("sampled_at < ?", cutoff).Delete(&BalanceSnapshot{})
		deleted = res.RowsAffected
		return res.Error
	})
	return deleted, err
}

// BalanceHistory 倒序拉取余额历史。
func (r *Rates) BalanceHistory(channelID uint, limit int) ([]BalanceSnapshot, error) {
	if limit <= 0 {
		limit = 100
	}
	var list []BalanceSnapshot
	if err := r.db.
		Where("channel_id = ?", channelID).
		Order("sampled_at DESC").
		Limit(limit).
		Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// DailyAggregate 一天的聚合余额（所有渠道之和）。
type DailyAggregate struct {
	Day     time.Time `json:"day"`
	Balance float64   `json:"balance"`
}

// AggregateBalanceTrend 取最近 N 天的"日内最后一次余额"按渠道之和，作为总余额趋势。
//
// 实现：对每个 (channel_id, day) 取该天最后一次 BalanceSnapshot 的余额，再按 day 求和。
// 没有采样的日子返回 0；调用方应自己外推 / 留空。
func (r *Rates) AggregateBalanceTrend(days int) ([]DailyAggregate, error) {
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return r.AggregateBalanceTrendSince(start.AddDate(0, 0, -(days - 1)))
}

func (r *Rates) AggregateBalanceTrendSince(since time.Time) ([]DailyAggregate, error) {
	type row struct {
		Day     time.Time
		Balance float64
	}
	var rows []row
	err := r.db.Raw(`
		WITH per_day AS (
			SELECT
				channel_id,
				date_trunc('day', sampled_at) AS day,
				MAX(sampled_at)               AS last_at
			FROM balance_snapshots
			WHERE sampled_at >= ?
			GROUP BY channel_id, date_trunc('day', sampled_at)
		)
		SELECT pd.day AS day, SUM(bs.balance) AS balance
		FROM per_day pd
		JOIN balance_snapshots bs
		  ON bs.channel_id = pd.channel_id AND bs.sampled_at = pd.last_at
		GROUP BY pd.day
		ORDER BY pd.day ASC
	`, since).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]DailyAggregate, 0, len(rows))
	for _, r := range rows {
		out = append(out, DailyAggregate{Day: r.Day, Balance: r.Balance})
	}
	return out, nil
}

// AggregateBalanceTrendHourly 取最近 N 小时的"小时内最后一次余额"按渠道之和。
//
// 实现与日趋势一致：对每个 (channel_id, hour) 取该小时最后一次 BalanceSnapshot，
// 再按 hour 求和，用于展示一天内余额波动。
func (r *Rates) AggregateBalanceTrendHourly(hours int) ([]DailyAggregate, error) {
	if hours <= 0 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours-1) * time.Hour).Truncate(time.Hour)
	return r.AggregateBalanceTrendHourlySince(since)
}

func (r *Rates) AggregateBalanceTrendHourlySince(since time.Time) ([]DailyAggregate, error) {
	type row struct {
		Day     time.Time
		Balance float64
	}
	var rows []row
	err := r.db.Raw(`
		WITH per_hour AS (
			SELECT
				channel_id,
				date_trunc('hour', sampled_at) AS hour,
				MAX(sampled_at)                AS last_at
			FROM balance_snapshots
			WHERE sampled_at >= ?
			GROUP BY channel_id, date_trunc('hour', sampled_at)
		)
		SELECT ph.hour AS day, SUM(bs.balance) AS balance
		FROM per_hour ph
		JOIN balance_snapshots bs
		  ON bs.channel_id = ph.channel_id AND bs.sampled_at = ph.last_at
		GROUP BY ph.hour
		ORDER BY ph.hour ASC
	`, since).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]DailyAggregate, 0, len(rows))
	for _, r := range rows {
		out = append(out, DailyAggregate{Day: r.Day, Balance: r.Balance})
	}
	return out, nil
}
