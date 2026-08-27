package storage

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// RelayStations 管理中转站配置及其同步快照。
type RelayStations struct{ db *gorm.DB }

func NewRelayStations(db *gorm.DB) *RelayStations { return &RelayStations{db: db} }

func (r *RelayStations) List() ([]RelayStation, error) {
	var list []RelayStation
	err := r.db.Order("id ASC").Find(&list).Error
	return list, err
}

func (r *RelayStations) FindByID(id uint) (*RelayStation, error) {
	var station RelayStation
	if err := r.db.First(&station, id).Error; err != nil {
		return nil, err
	}
	return &station, nil
}

func (r *RelayStations) Create(station *RelayStation) error { return r.db.Create(station).Error }
func (r *RelayStations) Update(station *RelayStation) error { return r.db.Save(station).Error }
func (r *RelayStations) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&OperationLedgerEntry{}).Where("relay_station_id = ?", id).Update("relay_station_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Model(&LocalAccount{}).Where("relay_station_id = ?", id).Updates(map[string]any{
			"relay_station_id": nil, "relay_account_external_id": nil, "linked_at": nil,
			"status": gorm.Expr("CASE WHEN status = 'deployed' THEN 'ready' ELSE status END"),
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayAccountAdjustmentLog{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_account_id IN (?)", tx.Model(&RelayAccount{}).Select("id").Where("relay_station_id = ?", id)).Delete(&RelayAccountGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayAccountCostOverride{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayAccount{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayChannelMapping{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayChannelPricingChange{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_channel_id IN (?)", tx.Model(&RelayChannel{}).Select("id").Where("relay_station_id = ?", id)).Delete(&RelayChannelGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", id).Delete(&RelayChannel{}).Error; err != nil {
			return err
		}
		return tx.Delete(&RelayStation{}, id).Error
	})
}

func (r *RelayStations) SetSyncError(id uint, message string) error {
	return r.db.Model(&RelayStation{}).Where("id = ?", id).Update("last_error", message).Error
}

// ReplaceSnapshot 用一次成功同步的完整结果替换站点镜像。映射不随同步删除，
// 这样分组/渠道临时缺失后恢复时仍保留用户的显式配置。
// RelaySnapshotLink is intentionally keyed by remote IDs. RelayChannel and RelayGroup
// rows are replaced during a sync, while their external IDs remain stable.
type RelaySnapshotLink struct {
	ChannelExternalID int64
	GroupExternalID   int64
}

type RelayAccountSnapshotLink struct {
	AccountExternalID int64
	GroupExternalID   int64
}

func (r *RelayStations) ReplaceSnapshot(
	stationID uint,
	groups []RelayGroup,
	channels []RelayChannel,
	links []RelaySnapshotLink,
	accounts []RelayAccount,
	accountLinks []RelayAccountSnapshotLink,
	syncedAt time.Time,
) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var previous []RelayChannel
		if err := tx.Where("relay_station_id = ?", stationID).Find(&previous).Error; err != nil {
			return err
		}
		previousByExternalID := make(map[int64]RelayChannel, len(previous))
		for _, channel := range previous {
			previousByExternalID[channel.ExternalID] = channel
		}
		var previousGroups []RelayGroup
		if err := tx.Where("relay_station_id = ?", stationID).Find(&previousGroups).Error; err != nil {
			return err
		}
		previousGroupByExternalID := make(map[int64]RelayGroup, len(previousGroups))
		for _, item := range previousGroups {
			previousGroupByExternalID[item.ExternalID] = item
		}
		var previousAccounts []RelayAccount
		if err := tx.Where("relay_station_id = ?", stationID).Find(&previousAccounts).Error; err != nil {
			return err
		}
		previousAccountByExternalID := make(map[int64]RelayAccount, len(previousAccounts))
		for _, item := range previousAccounts {
			previousAccountByExternalID[item.ExternalID] = item
		}
		if err := tx.Where("relay_account_id IN (?)", tx.Model(&RelayAccount{}).Select("id").Where("relay_station_id = ?", stationID)).Delete(&RelayAccountGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", stationID).Delete(&RelayAccount{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_channel_id IN (?)", tx.Model(&RelayChannel{}).Select("id").Where("relay_station_id = ?", stationID)).Delete(&RelayChannelGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", stationID).Delete(&RelayGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_station_id = ?", stationID).Delete(&RelayChannel{}).Error; err != nil {
			return err
		}
		if len(groups) > 0 {
			for i := range groups {
				if previous, ok := previousGroupByExternalID[groups[i].ExternalID]; ok {
					groups[i].ModelTypesJSON = previous.ModelTypesJSON
					groups[i].MonitorEnabled = previous.MonitorEnabled
				}
			}
			if err := tx.Create(&groups).Error; err != nil {
				return err
			}
		}
		changes := make([]RelayChannelPricingChange, 0)
		for i := range channels {
			channel := &channels[i]
			previous, ok := previousByExternalID[channel.ExternalID]
			if !ok {
				continue
			}
			channel.PricingChangedAt = previous.PricingChangedAt
			if previous.PricingHash == "" || previous.PricingHash == channel.PricingHash {
				continue
			}
			changedAt := syncedAt
			channel.PricingChangedAt = &changedAt
			changes = append(changes, RelayChannelPricingChange{
				RelayStationID: stationID, RelayChannelExternalID: channel.ExternalID,
				OldPricingJSON: previous.PricingJSON, NewPricingJSON: channel.PricingJSON,
				OldModelCount: previous.PricingModelCount, NewModelCount: channel.PricingModelCount,
				OldRuleCount: previous.PricingRuleCount, NewRuleCount: channel.PricingRuleCount,
				ChangedAt: syncedAt,
			})
		}
		if len(channels) > 0 {
			if err := tx.Create(&channels).Error; err != nil {
				return err
			}
		}
		if len(accounts) > 0 {
			for i := range accounts {
				if previous, ok := previousAccountByExternalID[accounts[i].ExternalID]; ok {
					accounts[i].ModelType = previous.ModelType
					if accounts[i].RateMultiplier == nil && previous.RateMultiplier != nil && previous.RateSource == "upstream_probe" {
						value := *previous.RateMultiplier
						accounts[i].RateMultiplier = &value
						accounts[i].RateSource = previous.RateSource
						accounts[i].RateObservedAt = previous.RateObservedAt
					}
				}
			}
			if err := tx.Create(&accounts).Error; err != nil {
				return err
			}
		}
		if len(changes) > 0 {
			if err := tx.Create(&changes).Error; err != nil {
				return err
			}
		}
		if len(links) > 0 {
			var persistedGroups []RelayGroup
			if err := tx.Where("relay_station_id = ?", stationID).Find(&persistedGroups).Error; err != nil {
				return err
			}
			var persistedChannels []RelayChannel
			if err := tx.Where("relay_station_id = ?", stationID).Find(&persistedChannels).Error; err != nil {
				return err
			}
			groupByExternalID := make(map[int64]uint, len(persistedGroups))
			for _, group := range persistedGroups {
				groupByExternalID[group.ExternalID] = group.ID
			}
			channelByExternalID := make(map[int64]uint, len(persistedChannels))
			for _, channel := range persistedChannels {
				channelByExternalID[channel.ExternalID] = channel.ID
			}
			resolved := make([]RelayChannelGroup, 0, len(links))
			for _, link := range links {
				channelID, channelOK := channelByExternalID[link.ChannelExternalID]
				groupID, groupOK := groupByExternalID[link.GroupExternalID]
				if channelOK && groupOK {
					resolved = append(resolved, RelayChannelGroup{RelayChannelID: channelID, RelayGroupID: groupID})
				}
			}
			if len(resolved) > 0 {
				if err := tx.Create(&resolved).Error; err != nil {
					return err
				}
			}
		}
		if len(accountLinks) > 0 {
			var persistedGroups []RelayGroup
			if err := tx.Where("relay_station_id = ?", stationID).Find(&persistedGroups).Error; err != nil {
				return err
			}
			var persistedAccounts []RelayAccount
			if err := tx.Where("relay_station_id = ?", stationID).Find(&persistedAccounts).Error; err != nil {
				return err
			}
			groupByExternalID := make(map[int64]uint, len(persistedGroups))
			for _, group := range persistedGroups {
				groupByExternalID[group.ExternalID] = group.ID
			}
			accountByExternalID := make(map[int64]uint, len(persistedAccounts))
			for _, account := range persistedAccounts {
				accountByExternalID[account.ExternalID] = account.ID
			}
			resolved := make([]RelayAccountGroup, 0, len(accountLinks))
			for _, link := range accountLinks {
				accountID, accountOK := accountByExternalID[link.AccountExternalID]
				groupID, groupOK := groupByExternalID[link.GroupExternalID]
				if accountOK && groupOK {
					resolved = append(resolved, RelayAccountGroup{RelayAccountID: accountID, RelayGroupID: groupID})
				}
			}
			if len(resolved) > 0 {
				if err := tx.Create(&resolved).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&RelayStation{}).Where("id = ?", stationID).Updates(map[string]any{
			"last_synced_at": syncedAt,
			"last_error":     "",
		}).Error
	})
}

func (r *RelayStations) ListPricingChanges(stationID uint, since time.Time, limit int) ([]RelayChannelPricingChange, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []RelayChannelPricingChange
	err := r.db.Where("relay_station_id = ? AND changed_at >= ?", stationID, since).
		Order("changed_at DESC").Limit(limit).Find(&list).Error
	return list, err
}

func (r *RelayStations) ListGroups(stationID uint) ([]RelayGroup, error) {
	var list []RelayGroup
	err := r.db.Where("relay_station_id = ?", stationID).Order("sort_order ASC").Order("external_id ASC").Find(&list).Error
	return list, err
}

// UpdateGroupSortOrders persists the complete order for one station.
func (r *RelayStations) UpdateGroupSortOrders(stationID uint, updates map[int64]int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for externalID, sortOrder := range updates {
			if err := tx.Model(&RelayGroup{}).
				Where("relay_station_id = ? AND external_id = ?", stationID, externalID).
				Update("sort_order", sortOrder).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *RelayStations) FindGroupByExternalID(stationID uint, externalID int64) (*RelayGroup, error) {
	var group RelayGroup
	if err := r.db.Where("relay_station_id = ? AND external_id = ?", stationID, externalID).First(&group).Error; err != nil {
		return nil, err
	}
	return &group, nil
}

func (r *RelayStations) UpdateGroup(group *RelayGroup) error {
	return r.db.Model(&RelayGroup{}).
		Where("relay_station_id = ? AND external_id = ?", group.RelayStationID, group.ExternalID).
		Updates(map[string]any{
			"name": group.Name, "rate_multiplier": group.RateMultiplier,
			"is_exclusive": group.IsExclusive, "status": group.Status,
			"description": group.Description, "model_types_json": group.ModelTypesJSON,
			"monitor_enabled": group.MonitorEnabled,
			"synced_at":       group.SyncedAt,
		}).Error
}

// UpdateGroupAndSyncAccountModelType updates a group and carries a
// deterministic single-type transition to its linked accounts in one
// transaction. Accounts that have been manually rebound to another type are
// left untouched.
func (r *RelayStations) UpdateGroupAndSyncAccountModelType(group *RelayGroup, oldType, newType string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&RelayGroup{}).
			Where("relay_station_id = ? AND external_id = ?", group.RelayStationID, group.ExternalID).
			Updates(map[string]any{
				"name": group.Name, "rate_multiplier": group.RateMultiplier,
				"is_exclusive": group.IsExclusive, "status": group.Status,
				"description": group.Description, "model_types_json": group.ModelTypesJSON,
				"monitor_enabled": group.MonitorEnabled,
				"synced_at":       group.SyncedAt,
			}).Error; err != nil {
			return err
		}

		accountQuery := tx.Model(&RelayAccount{}).
			Where("relay_station_id = ? AND id IN (?)", group.RelayStationID,
				tx.Table("relay_account_groups").Select("relay_account_id").Where("relay_group_id = ?", group.ID))
		if strings.TrimSpace(oldType) == "" {
			accountQuery = accountQuery.Where("model_type IS NULL OR BTRIM(model_type) = ''")
		} else {
			accountQuery = accountQuery.Where("LOWER(BTRIM(model_type)) = ?", strings.ToLower(strings.TrimSpace(oldType)))
		}
		return accountQuery.Update("model_type", strings.ToLower(strings.TrimSpace(newType))).Error
	})
}

func (r *RelayStations) ListChannels(stationID uint) ([]RelayChannel, error) {
	var list []RelayChannel
	err := r.db.Where("relay_station_id = ?", stationID).Order("name ASC").Find(&list).Error
	return list, err
}

func (r *RelayStations) ListAccounts(stationID uint) ([]RelayAccount, error) {
	var list []RelayAccount
	err := r.db.Where("relay_station_id = ?", stationID).Order("name ASC, external_id ASC").Find(&list).Error
	return list, err
}

func (r *RelayStations) ListCostOverrides(stationID uint) ([]RelayAccountCostOverride, error) {
	var list []RelayAccountCostOverride
	err := r.db.Where("relay_station_id = ?", stationID).
		Order("relay_account_external_id ASC").Find(&list).Error
	return list, err
}

// ChannelRateBoundAccount is an explicit account-to-channel-group cost
// binding. Account snapshots can be temporarily absent, so the external ID is
// kept as the stable identifier and the name is optional.
type ChannelRateBoundAccount struct {
	UpstreamGroup          string
	RelayStationID         uint
	RelayStationName       string
	RelayAccountExternalID int64
	RelayAccountName       string
}

// ListChannelRateBoundAccounts lists user bindings and automatically matched
// account bindings for one monitored channel, grouped by the caller using
// UpstreamGroup.
func (r *RelayStations) ListChannelRateBoundAccounts(channelID uint) ([]ChannelRateBoundAccount, error) {
	var list []ChannelRateBoundAccount
	err := r.db.Raw(`
		SELECT
			o.upstream_group,
			o.relay_station_id,
			s.name AS relay_station_name,
			o.relay_account_external_id,
			COALESCE(a.name, '') AS relay_account_name
		FROM relay_account_cost_overrides AS o
		JOIN relay_stations AS s ON s.id = o.relay_station_id
		LEFT JOIN relay_accounts AS a
			ON a.relay_station_id = o.relay_station_id
			AND a.external_id = o.relay_account_external_id
		WHERE o.mode IN ('channel_group', 'auto_link')
			AND o.monitor_channel_id = ?
			AND BTRIM(o.upstream_group) <> ''
		ORDER BY o.upstream_group ASC, s.name ASC, a.name ASC NULLS LAST, o.relay_account_external_id ASC
	`, channelID).Scan(&list).Error
	return list, err
}

func (r *RelayStations) FindCostOverride(stationID uint, accountExternalID int64) (*RelayAccountCostOverride, error) {
	var item RelayAccountCostOverride
	if err := r.db.Where("relay_station_id = ? AND relay_account_external_id = ?", stationID, accountExternalID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (r *RelayStations) UpsertCostOverride(item *RelayAccountCostOverride) error {
	return r.db.Where("relay_station_id = ? AND relay_account_external_id = ?", item.RelayStationID, item.RelayAccountExternalID).
		Assign(map[string]any{
			"mode": item.Mode, "monitor_channel_id": item.MonitorChannelID,
			"upstream_group": item.UpstreamGroup, "manual_multiplier": item.ManualMultiplier,
		}).FirstOrCreate(item).Error
}

func (r *RelayStations) DeleteCostOverride(stationID uint, accountExternalID int64) error {
	return r.db.Where("relay_station_id = ? AND relay_account_external_id = ?", stationID, accountExternalID).
		Delete(&RelayAccountCostOverride{}).Error
}

// AutomaticChannelAccountBinding is the durable form of a manual-channel
// URL match. The relay account is the source of truth for both its generated
// channel group name and multiplier.
type AutomaticChannelAccountBinding struct {
	ChannelID              uint
	RelayStationID         uint
	RelayStationName       string
	RelayAccountExternalID int64
	RelayAccountName       string
	RateMultiplier         float64
	RateObservedAt         *time.Time
}

func automaticBindingKey(stationID uint, accountExternalID int64) string {
	return fmt.Sprintf("%d:%d", stationID, accountExternalID)
}

// ReconcileAutomaticChannelAccountBindings atomically makes relay-account
// matches visible as read-only channel groups and as explicit cost bindings.
// It only owns rows marked auto_link / relay_account; a user-created
// channel_group binding always remains untouched.
func (r *RelayStations) ReconcileAutomaticChannelAccountBindings(bindings []AutomaticChannelAccountBinding) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		wanted := make(map[string]AutomaticChannelAccountBinding, len(bindings))
		for _, binding := range bindings {
			if binding.ChannelID == 0 || binding.RelayStationID == 0 || binding.RelayAccountExternalID <= 0 || binding.RateMultiplier < 0 {
				continue
			}
			wanted[automaticBindingKey(binding.RelayStationID, binding.RelayAccountExternalID)] = binding
		}

		var existingOverrides []RelayAccountCostOverride
		if err := tx.Where("mode = ?", "auto_link").Find(&existingOverrides).Error; err != nil {
			return err
		}
		for _, override := range existingOverrides {
			binding, ok := wanted[automaticBindingKey(override.RelayStationID, override.RelayAccountExternalID)]
			if ok && override.MonitorChannelID != nil && *override.MonitorChannelID == binding.ChannelID {
				continue
			}
			if err := tx.Delete(&override).Error; err != nil {
				return err
			}
		}

		var existingRates []RateSnapshot
		if err := tx.Where("source = ?", RateSnapshotSourceRelayAccount).Find(&existingRates).Error; err != nil {
			return err
		}
		rateByBinding := make(map[string]RateSnapshot, len(existingRates))
		for _, rate := range existingRates {
			if rate.RelayStationID == nil || rate.RelayAccountExternalID == nil {
				if err := tx.Delete(&rate).Error; err != nil {
					return err
				}
				continue
			}
			key := automaticBindingKey(*rate.RelayStationID, *rate.RelayAccountExternalID)
			binding, ok := wanted[key]
			if !ok || rate.ChannelID != binding.ChannelID {
				if err := tx.Delete(&rate).Error; err != nil {
					return err
				}
				continue
			}
			rateByBinding[key] = rate
		}

		for key, binding := range wanted {
			var existingOverride RelayAccountCostOverride
			overrideErr := tx.Where("relay_station_id = ? AND relay_account_external_id = ?", binding.RelayStationID, binding.RelayAccountExternalID).
				First(&existingOverride).Error
			if overrideErr != nil && overrideErr != gorm.ErrRecordNotFound {
				return overrideErr
			}
			// A manual association is an explicit operator decision. It prevents
			// this automatic match from replacing its group or multiplier.
			if overrideErr == nil && existingOverride.Mode != "auto_link" {
				continue
			}

			existing, found := rateByBinding[key]
			groupName, err := automaticRelayAccountGroupName(tx, binding, func() uint {
				if found {
					return existing.ID
				}
				return 0
			}())
			if err != nil {
				return err
			}
			description := automaticRelayAccountGroupDescription(binding)
			now := time.Now().UTC()
			if !found {
				stationID, accountID := binding.RelayStationID, binding.RelayAccountExternalID
				existing = RateSnapshot{
					ChannelID: binding.ChannelID, ModelName: groupName, Description: description,
					Ratio: binding.RateMultiplier, CompletionRatio: binding.RateMultiplier,
					Source: RateSnapshotSourceRelayAccount, RelayStationID: &stationID, RelayAccountExternalID: &accountID,
					FirstSeenAt: now, LastSeenAt: now,
				}
				if err := tx.Create(&existing).Error; err != nil {
					return err
				}
			} else {
				oldRatio, oldCompletion := existing.Ratio, existing.CompletionRatio
				if err := tx.Model(&existing).Updates(map[string]any{
					"model_name": groupName, "description": description,
					"ratio": binding.RateMultiplier, "completion_ratio": binding.RateMultiplier,
					"last_seen_at": now,
				}).Error; err != nil {
					return err
				}
				if oldRatio != binding.RateMultiplier || oldCompletion != binding.RateMultiplier {
					if err := tx.Create(&RateChangeLog{
						ChannelID: binding.ChannelID, ModelName: groupName,
						OldRatio: &oldRatio, NewRatio: binding.RateMultiplier,
						OldCompletionRatio: &oldCompletion, NewCompletionRatio: binding.RateMultiplier,
						ChangedAt: now,
					}).Error; err != nil {
						return err
					}
				}
			}

			channelID := binding.ChannelID
			if overrideErr == gorm.ErrRecordNotFound {
				override := RelayAccountCostOverride{
					RelayStationID: binding.RelayStationID, RelayAccountExternalID: binding.RelayAccountExternalID,
					Mode: "auto_link", MonitorChannelID: &channelID, UpstreamGroup: groupName,
				}
				if err := tx.Create(&override).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&existingOverride).Updates(map[string]any{
				"monitor_channel_id": &channelID, "upstream_group": groupName, "manual_multiplier": nil,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func automaticRelayAccountGroupName(tx *gorm.DB, binding AutomaticChannelAccountBinding, currentID uint) (string, error) {
	base := strings.TrimSpace(binding.RelayAccountName)
	if base == "" {
		base = fmt.Sprintf("账号 #%d", binding.RelayAccountExternalID)
	}
	candidates := []string{base, fmt.Sprintf("%s (#%d)", base, binding.RelayAccountExternalID), fmt.Sprintf("%s (#%d-%d)", base, binding.RelayStationID, binding.RelayAccountExternalID)}
	for _, candidate := range candidates {
		candidate = trimRateSnapshotName(candidate)
		var conflict RateSnapshot
		err := tx.Where("channel_id = ? AND model_name = ?", binding.ChannelID, candidate).First(&conflict).Error
		if err == gorm.ErrRecordNotFound || (err == nil && conflict.ID == currentID) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("渠道 %d 已存在与中转账号 %d 冲突的自动分组名称", binding.ChannelID, binding.RelayAccountExternalID)
}

func trimRateSnapshotName(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= 256 {
		return value
	}
	return strings.TrimSpace(string(runes[:256]))
}

func automaticRelayAccountGroupDescription(binding AutomaticChannelAccountBinding) string {
	name := strings.TrimSpace(binding.RelayStationName)
	if name == "" {
		name = fmt.Sprintf("中转站 #%d", binding.RelayStationID)
	}
	return fmt.Sprintf("自动关联 %s / 账号 #%d", name, binding.RelayAccountExternalID)
}

func (r *RelayStations) FindAccountByExternalID(stationID uint, externalID int64) (*RelayAccount, error) {
	var account RelayAccount
	if err := r.db.Where("relay_station_id = ? AND external_id = ?", stationID, externalID).First(&account).Error; err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *RelayStations) UpdateAccountProbe(stationID uint, externalID int64, account RelayAccount) error {
	return r.db.Model(&RelayAccount{}).
		Where("relay_station_id = ? AND external_id = ?", stationID, externalID).
		Updates(map[string]any{
			"name": account.Name, "base_url": account.BaseURL,
			"platform": account.Platform, "type": account.Type, "status": account.Status,
			"schedulable": account.Schedulable, "concurrency": account.Concurrency, "priority": account.Priority,
			"pool_mode": account.PoolMode, "pool_mode_retry_count": account.PoolModeRetryCount,
			"account_plan": account.AccountPlan, "last_used_at": account.LastUsedAt,
			"rate_multiplier": account.RateMultiplier,
			"rate_source":     account.RateSource, "rate_observed_at": account.RateObservedAt,
			"synced_at": account.SyncedAt,
		}).Error
}

type RelayAccountRateUpdate struct {
	ExternalID     int64
	RateMultiplier *float64
	RateSource     string
	RateObservedAt *time.Time
}

// UpdateAccountRates changes only probe-derived billing fields so a rate job
// cannot replace group, scheduling, pricing, or latency snapshot data.
func (r *RelayStations) UpdateAccountRates(stationID uint, updates []RelayAccountRateUpdate, syncedAt time.Time) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, update := range updates {
			values := map[string]any{"synced_at": syncedAt}
			if update.RateMultiplier != nil {
				values["rate_multiplier"] = update.RateMultiplier
				values["rate_source"] = update.RateSource
				values["rate_observed_at"] = update.RateObservedAt
			}
			if err := tx.Model(&RelayAccount{}).
				Where("relay_station_id = ? AND external_id = ?", stationID, update.ExternalID).
				Updates(values).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *RelayStations) ListAccountLinks(stationID uint) ([]RelayAccountGroup, error) {
	var list []RelayAccountGroup
	err := r.db.Table("relay_account_groups").
		Joins("JOIN relay_accounts ON relay_accounts.id = relay_account_groups.relay_account_id").
		Where("relay_accounts.relay_station_id = ?", stationID).
		Find(&list).Error
	return list, err
}

func (r *RelayStations) SetAccountGroups(stationID uint, accountExternalID int64, groupExternalIDs []int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var account RelayAccount
		if err := tx.Where("relay_station_id = ? AND external_id = ?", stationID, accountExternalID).First(&account).Error; err != nil {
			return err
		}
		if err := tx.Where("relay_account_id = ?", account.ID).Delete(&RelayAccountGroup{}).Error; err != nil {
			return err
		}
		if len(groupExternalIDs) == 0 {
			return nil
		}
		var groups []RelayGroup
		if err := tx.Where("relay_station_id = ? AND external_id IN ?", stationID, groupExternalIDs).Find(&groups).Error; err != nil {
			return err
		}
		byExternalID := make(map[int64]uint, len(groups))
		for _, group := range groups {
			byExternalID[group.ExternalID] = group.ID
		}
		links := make([]RelayAccountGroup, 0, len(groupExternalIDs))
		for _, externalID := range groupExternalIDs {
			if groupID, ok := byExternalID[externalID]; ok {
				links = append(links, RelayAccountGroup{RelayAccountID: account.ID, RelayGroupID: groupID})
			}
		}
		if len(links) > 0 {
			return tx.Create(&links).Error
		}
		return nil
	})
}

func (r *RelayStations) SetAccountSchedulable(stationID uint, accountExternalID int64, schedulable bool) error {
	return r.db.Model(&RelayAccount{}).
		Where("relay_station_id = ? AND external_id = ?", stationID, accountExternalID).
		Update("schedulable", schedulable).Error
}

func (r *RelayStations) SetAccountRuntimeSettings(stationID uint, accountExternalID int64, concurrency, priority *int, retryCount ...*int) error {
	updates := make(map[string]any, 3)
	if concurrency != nil {
		updates["concurrency"] = *concurrency
	}
	if priority != nil {
		updates["priority"] = *priority
	}
	if len(retryCount) > 0 && retryCount[0] != nil {
		updates["pool_mode_retry_count"] = *retryCount[0]
	}
	if len(updates) == 0 {
		return nil
	}
	return r.db.Model(&RelayAccount{}).
		Where("relay_station_id = ? AND external_id = ?", stationID, accountExternalID).
		Updates(updates).Error
}

func (r *RelayStations) SetAccountModelTypes(stationID uint, accountExternalIDs []int64, modelType string) error {
	modelType = strings.TrimSpace(modelType)
	if len(accountExternalIDs) == 0 {
		return nil
	}
	return r.db.Model(&RelayAccount{}).Where("relay_station_id = ? AND external_id IN ?", stationID, accountExternalIDs).Update("model_type", modelType).Error
}

func (r *RelayStations) CreateAdjustmentLog(log *RelayAccountAdjustmentLog) error {
	return r.db.Create(log).Error
}

func (r *RelayStations) ListAdjustmentLogs(stationID uint, limit int) ([]RelayAccountAdjustmentLog, error) {
	if limit <= 0 {
		limit = 50
	}
	var list []RelayAccountAdjustmentLog
	err := r.db.Where("relay_station_id = ?", stationID).Order("applied_at DESC").Limit(limit).Find(&list).Error
	return list, err
}

// ListAdjustmentLogsByCategory returns the newest records for each tracked UI
// category independently, so a busy category cannot evict another one. Manual
// concurrency and retry-count changes are intentionally excluded. Historical
// combined runtime rows are reduced to their priority change.
func (r *RelayStations) ListAdjustmentLogsByCategory(stationID uint, limitPerCategory int) ([]RelayAccountAdjustmentLog, error) {
	if limitPerCategory <= 0 {
		limitPerCategory = 50
	}
	byID := make(map[uint]RelayAccountAdjustmentLog, limitPerCategory*3)
	runtimeActions := []string{"enable_scheduling", "disable_scheduling", "priority_update", "concurrency_update", "runtime_settings_update", "retry_count_update"}
	var groupRows []RelayAccountAdjustmentLog
	if err := r.db.Where("relay_station_id = ? AND action NOT IN ?", stationID, runtimeActions).
		Order("applied_at DESC").Limit(limitPerCategory).Find(&groupRows).Error; err != nil {
		return nil, err
	}
	for _, row := range groupRows {
		byID[row.ID] = row
	}
	var schedulingRows []RelayAccountAdjustmentLog
	if err := r.db.Where("relay_station_id = ? AND action IN ?", stationID, []string{"enable_scheduling", "disable_scheduling"}).
		Order("applied_at DESC").Limit(limitPerCategory).Find(&schedulingRows).Error; err != nil {
		return nil, err
	}
	for _, row := range schedulingRows {
		byID[row.ID] = row
	}
	var priorityRows []RelayAccountAdjustmentLog
	if err := r.db.Where("relay_station_id = ? AND (action = ? OR (action = ? AND old_priority IS NOT NULL AND new_priority IS NOT NULL))", stationID, "priority_update", "runtime_settings_update").
		Order("applied_at DESC").Limit(limitPerCategory).Find(&priorityRows).Error; err != nil {
		return nil, err
	}
	for _, row := range priorityRows {
		if row.Action == "runtime_settings_update" {
			row.Action = "priority_update"
			row.OldConcurrency = nil
			row.NewConcurrency = nil
			row.OldPoolModeRetryCount = nil
			row.NewPoolModeRetryCount = nil
		}
		byID[row.ID] = row
	}
	list := make([]RelayAccountAdjustmentLog, 0, len(byID))
	for _, row := range byID {
		list = append(list, row)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].AppliedAt.Equal(list[j].AppliedAt) {
			return list[i].ID > list[j].ID
		}
		return list[i].AppliedAt.After(list[j].AppliedAt)
	})
	return list, nil
}

func (r *RelayStations) FindChannelByExternalID(stationID uint, externalID int64) (*RelayChannel, error) {
	var channel RelayChannel
	if err := r.db.Where("relay_station_id = ? AND external_id = ?", stationID, externalID).First(&channel).Error; err != nil {
		return nil, err
	}
	return &channel, nil
}

func (r *RelayStations) ListLinks(stationID uint) ([]RelayChannelGroup, error) {
	var list []RelayChannelGroup
	err := r.db.Table("relay_channel_groups").
		Joins("JOIN relay_channels ON relay_channels.id = relay_channel_groups.relay_channel_id").
		Where("relay_channels.relay_station_id = ?", stationID).
		Find(&list).Error
	return list, err
}

func (r *RelayStations) ListMappings(stationID uint) ([]RelayChannelMapping, error) {
	var list []RelayChannelMapping
	err := r.db.Where("relay_station_id = ?", stationID).Order("relay_channel_external_id ASC").Find(&list).Error
	return list, err
}

func (r *RelayStations) UpsertMapping(mapping *RelayChannelMapping) error {
	return r.db.Where("relay_station_id = ? AND relay_channel_external_id = ?", mapping.RelayStationID, mapping.RelayChannelExternalID).
		Assign(map[string]any{
			"monitor_channel_id": mapping.MonitorChannelID,
			"upstream_group":     mapping.UpstreamGroup,
		}).
		FirstOrCreate(mapping).Error
}

func (r *RelayStations) DeleteMapping(stationID uint, externalID int64) error {
	return r.db.Where("relay_station_id = ? AND relay_channel_external_id = ?", stationID, externalID).
		Delete(&RelayChannelMapping{}).Error
}

func (r *RelayStations) ClearMonitorChannel(channelID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&RelayChannelMapping{}).Where("monitor_channel_id = ?", channelID).Updates(map[string]any{
			"monitor_channel_id": nil,
			"upstream_group":     "",
		}).Error; err != nil {
			return err
		}
		return tx.Where("monitor_channel_id = ?", channelID).Delete(&RelayAccountCostOverride{}).Error
	})
}
