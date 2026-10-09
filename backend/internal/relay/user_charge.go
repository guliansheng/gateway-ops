package relay

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// SetUserChargeExcludedBatch 批量设置账号是否计入用户扣费统计。这是纯 Hub 侧的
// 运营口径：远端中转站和账号自身的扣费明细保持不变，只有首页 KPI、运营收支等
// 汇总会剔除这些账号，用于“账号 A 实际走已关联渠道 B，避免重复统计”的场景。
func (s *Service) SetUserChargeExcludedBatch(stationID uint, accountExternalIDs []int64, excluded bool) (AccountBatchActionResult, error) {
	if _, err := s.stations.FindByID(stationID); err != nil {
		return AccountBatchActionResult{}, err
	}
	accounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return AccountBatchActionResult{}, err
	}
	accountByExternalID := make(map[int64]storage.RelayAccount, len(accounts))
	for _, account := range accounts {
		accountByExternalID[account.ExternalID] = account
	}

	result := AccountBatchActionResult{}
	changed := make([]int64, 0, len(accountExternalIDs))
	for _, accountID := range uniqueAccountIDs(accountExternalIDs) {
		result.Requested++
		account, ok := accountByExternalID[accountID]
		if !ok {
			result.addFailure(accountID, errors.New("账号不存在或快照已过期"))
			continue
		}
		if account.UserChargeExcluded == excluded {
			result.Skipped++
			continue
		}
		changed = append(changed, accountID)
	}
	if len(changed) == 0 {
		return result, nil
	}
	if err := s.stations.SetAccountsUserChargeExcluded(stationID, changed, excluded); err != nil {
		return result, err
	}
	result.Applied += len(changed)
	return result, nil
}

// ExcludedUserChargeSum 汇总被排除在用户扣费统计之外的账号扣费。
type ExcludedUserChargeSum struct {
	// Total 是所有被排除账号的扣费，首页合计与运营收入需要减去它。
	Total float64
	// Failed 是读取不到扣费明细的被排除账号数，>0 表示扣减不完整。
	Failed int
}

// ExcludedUserCharge 返回被标记为“不计入用户扣费统计”的账号扣费。渠道侧的
// 用户扣费在 ChannelUsageTotals 里直接跳过这些账号，只有按站点汇总的合计与
// 运营收入需要在这里显式扣减。
//
// 扣费数据仍按账号原样保存，这里只是为汇总口径重新计算一遍可剔除的金额。
func (s *Service) ExcludedUserCharge(ctx context.Context, rangeName string, since time.Time) (ExcludedUserChargeSum, error) {
	stations, err := s.stations.List()
	if err != nil {
		return ExcludedUserChargeSum{}, err
	}
	result := ExcludedUserChargeSum{}
	var failures []error
	for _, station := range stations {
		accounts, err := s.stations.ListAccounts(station.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("station %d: %w", station.ID, err))
			continue
		}
		excludedIDs := make([]int64, 0)
		for _, account := range accounts {
			if account.UserChargeExcluded {
				excludedIDs = append(excludedIDs, account.ExternalID)
			}
		}
		if len(excludedIDs) == 0 {
			continue
		}
		usage, usageErr := s.usageStatsForAccountIDs(ctx, station.ID, rangeName, since, excludedIDs)
		if usageErr != nil {
			failures = append(failures, fmt.Errorf("station %d: %w", station.ID, usageErr))
		}
		for _, accountID := range excludedIDs {
			stats, ok := usage.Accounts[accountID]
			if !ok {
				result.Failed++
				continue
			}
			result.Total += stats.UserCharge
		}
	}
	return result, errors.Join(failures...)
}

// UnlinkedAccountCharge 是首页“未关联渠道账号扣费”的单账号构成明细。
type UnlinkedAccountCharge struct {
	StationID          uint    `json:"station_id"`
	StationName        string  `json:"station_name"`
	AccountExternalID  int64   `json:"account_external_id"`
	AccountName        string  `json:"account_name"`
	UserCharge         float64 `json:"user_charge"`
	UserChargeExcluded bool    `json:"user_charge_excluded"`
	Failed             bool    `json:"failed"`
}

// UnlinkedAccounts 列出没有归属到任何监控渠道的中转站账号及其区间用户扣费。
// 账号与渠道的绑定规则与 ChannelUsageTotals 完全一致，所以这里的明细可以解释
// 首页拆分中的“未关联渠道账号扣费”。返回值在部分账号读取失败时仍可用，
// 失败项用 Failed 标记，同时以 error 汇总原因。
func (s *Service) UnlinkedAccounts(ctx context.Context, channels []storage.Channel, rangeName string, since time.Time) ([]UnlinkedAccountCharge, error) {
	stations, err := s.stations.List()
	if err != nil {
		return nil, err
	}
	result := make([]UnlinkedAccountCharge, 0)
	var failures []error
	for _, station := range stations {
		overrides, err := s.stations.ListCostOverrides(station.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("station %d: %w", station.ID, err))
			continue
		}
		accounts, err := s.stations.ListAccounts(station.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("station %d: %w", station.ID, err))
			continue
		}
		bindings := resolveChannelUsageCostBindings(channels, accounts, overrides, nil)
		unlinked := unlinkedAccounts(accounts, bindings)
		if len(unlinked) == 0 {
			continue
		}
		unlinkedIDs := make([]int64, 0, len(unlinked))
		for _, account := range unlinked {
			unlinkedIDs = append(unlinkedIDs, account.ExternalID)
		}
		usage, usageErr := s.usageStatsForAccountIDs(ctx, station.ID, rangeName, since, unlinkedIDs)
		if usageErr != nil {
			failures = append(failures, fmt.Errorf("station %d: %w", station.ID, usageErr))
		}
		for _, account := range unlinked {
			item := UnlinkedAccountCharge{
				StationID:          station.ID,
				StationName:        station.Name,
				AccountExternalID:  account.ExternalID,
				AccountName:        account.Name,
				UserChargeExcluded: account.UserChargeExcluded,
			}
			stats, ok := usage.Accounts[account.ExternalID]
			if usageErr != nil || !ok {
				item.Failed = true
			} else {
				item.UserCharge = stats.UserCharge
			}
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].UserCharge != result[j].UserCharge {
			return result[i].UserCharge > result[j].UserCharge
		}
		if result[i].StationID != result[j].StationID {
			return result[i].StationID < result[j].StationID
		}
		return result[i].AccountExternalID < result[j].AccountExternalID
	})
	return result, errors.Join(failures...)
}

// unlinkedAccounts 返回没有绑定到任何渠道的账号，顺序与入参一致。是否“不计入
// 用户扣费统计”不影响归属判定：被排除的账号仍然留在明细里，只是汇总时被扣掉。
func unlinkedAccounts(accounts []storage.RelayAccount, bindings map[int64]channelUsageCostBinding) []storage.RelayAccount {
	result := make([]storage.RelayAccount, 0, len(accounts))
	for _, account := range accounts {
		if _, bound := bindings[account.ExternalID]; bound {
			continue
		}
		result = append(result, account)
	}
	return result
}
