package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

const (
	RiskStateInactive        = "inactive"
	RiskStateCostUnknown     = "cost_unknown"
	RiskStateUnassigned      = "unassigned"
	RiskStateProtected       = "protected"
	RiskStateNoProfit        = "no_profit"
	RiskStateRisk            = "risk"
	RiskStateNoSafeCandidate = "no_safe_candidate"
)

// AccountRisk 是风险列表和调组执行共用的判定结果。
type AccountRisk struct {
	Account              storage.RelayAccount
	CurrentGroups        []storage.RelayGroup
	UnsafeGroups         []storage.RelayGroup
	NoProfitGroups       []storage.RelayGroup
	RecommendedGroup     *storage.RelayGroup
	DowngradeGroup       *storage.RelayGroup
	DowngradeGroups      []storage.RelayGroup
	SuggestedGroupIDs    []int64
	CurrentMinMultiplier *float64
	Margin               *float64
	State                string
	CanApply             bool
	CostOverride         *storage.RelayAccountCostOverride
}

type AccountBatchActionResult struct {
	Requested int      `json:"requested"`
	Applied   int      `json:"applied"`
	Skipped   int      `json:"skipped"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors,omitempty"`
}

func (s *Service) Risks(stationID uint) ([]AccountRisk, error) {
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return nil, err
	}
	accounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return nil, err
	}
	links, err := s.stations.ListAccountLinks(stationID)
	if err != nil {
		return nil, err
	}
	overrides, err := s.stations.ListCostOverrides(stationID)
	if err != nil {
		return nil, err
	}
	overrideByAccount := make(map[int64]storage.RelayAccountCostOverride, len(overrides))
	for _, item := range overrides {
		overrideByAccount[item.RelayAccountExternalID] = item
	}
	groupsByID := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		groupsByID[group.ID] = group
	}
	groupsByAccountID := make(map[uint][]storage.RelayGroup, len(accounts))
	groupTypes := make(map[uint]map[string]struct{})
	accountByID := make(map[uint]storage.RelayAccount, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	for _, link := range links {
		group, groupOK := groupsByID[link.RelayGroupID]
		account, accountOK := accountByID[link.RelayAccountID]
		if !groupOK || !accountOK {
			continue
		}
		groupsByAccountID[link.RelayAccountID] = append(groupsByAccountID[link.RelayAccountID], group)
		if groupTypes[group.ID] == nil {
			groupTypes[group.ID] = make(map[string]struct{})
		}
		groupTypes[group.ID][normalizedAccountType(account.Type)] = struct{}{}
	}

	// 同一同步快照内重复使用渠道倍率，避免批量风险列表触发大量查询。
	rateCache := make(map[uint][]storage.RateSnapshot)
	resolveOverride := func(item storage.RelayAccountCostOverride) (*float64, bool) {
		switch item.Mode {
		case "manual":
			if item.ManualMultiplier != nil && *item.ManualMultiplier >= 0 {
				return item.ManualMultiplier, true
			}
		case "channel_group", "auto_link":
			if item.MonitorChannelID == nil || strings.TrimSpace(item.UpstreamGroup) == "" || s.rates == nil {
				return nil, false
			}
			rates, ok := rateCache[*item.MonitorChannelID]
			if !ok {
				rates, err = s.rates.ListByChannel(*item.MonitorChannelID)
				if err != nil {
					return nil, false
				}
				rateCache[*item.MonitorChannelID] = rates
			}
			for _, rate := range rates {
				if rate.ModelName == item.UpstreamGroup && rate.Ratio >= 0 {
					value := rate.Ratio
					return &value, true
				}
			}
		}
		return nil, false
	}

	risks := make([]AccountRisk, 0, len(accounts))
	for _, account := range accounts {
		var override *storage.RelayAccountCostOverride
		if item, ok := overrideByAccount[account.ExternalID]; ok {
			copy := item
			override = &copy
			if cost, ok := resolveOverride(item); ok {
				account.RateMultiplier = cost
				account.RateSource = item.Mode
				observed := item.UpdatedAt
				account.RateObservedAt = &observed
			} else {
				account.RateMultiplier = nil
				account.RateSource = ""
			}
		} else if account.RateSource == "" {
			// Sub2API 的 rate_multiplier 默认值不是成本，未知成本不能按 1 倍参与计算。
			account.RateMultiplier = nil
		}
		risk := evaluateAccountRiskWithTypes(account, groupsByAccountID[account.ID], groups, groupTypes)
		risk.CostOverride = override
		risks = append(risks, risk)
	}
	return risks, nil
}

// 保留旧测试和外部包内调用的签名；利润空间参数已不再参与判定。
func evaluateAccountRisk(account storage.RelayAccount, currentGroups, allGroups []storage.RelayGroup, _ float64) AccountRisk {
	return evaluateAccountRiskWithTypes(account, currentGroups, allGroups, nil)
}

func evaluateAccountRiskWithTypes(account storage.RelayAccount, currentGroups, allGroups []storage.RelayGroup, groupTypes map[uint]map[string]struct{}) AccountRisk {
	return evaluateAccountRiskWithOptions(account, currentGroups, allGroups, groupTypes, false)
}

func evaluateAccountRiskWithOptions(account storage.RelayAccount, currentGroups, allGroups []storage.RelayGroup, groupTypes map[uint]map[string]struct{}, restrictModelCandidates bool) AccountRisk {
	risk := AccountRisk{Account: account, CurrentGroups: append([]storage.RelayGroup(nil), currentGroups...), State: RiskStateProtected}
	sortGroups(risk.CurrentGroups)
	if !isActive(account.Status) {
		risk.State = RiskStateInactive
		return risk
	}
	if account.RateMultiplier == nil || *account.RateMultiplier < 0 || account.RateSource == "" {
		risk.State = RiskStateCostUnknown
		return risk
	}
	cost := *account.RateMultiplier
	activeCurrent := make([]storage.RelayGroup, 0, len(currentGroups))
	for _, group := range currentGroups {
		if isActive(group.Status) && samePlatform(group.Platform, account.Platform) && sameAccountType(account, group, groupTypes) {
			activeCurrent = append(activeCurrent, group)
		}
	}
	if len(activeCurrent) == 0 {
		risk.State = RiskStateUnassigned
		return risk
	}
	sortGroups(activeCurrent)
	minCurrent := activeCurrent[0].RateMultiplier
	for _, group := range activeCurrent[1:] {
		if group.RateMultiplier < minCurrent {
			minCurrent = group.RateMultiplier
		}
	}
	risk.CurrentMinMultiplier = &minCurrent
	margin := minCurrent - cost
	risk.Margin = &margin

	for _, group := range activeCurrent {
		switch {
		case sameMultiplier(cost, group.RateMultiplier):
			risk.NoProfitGroups = append(risk.NoProfitGroups, group)
		case cost > group.RateMultiplier:
			risk.UnsafeGroups = append(risk.UnsafeGroups, group)
		}
	}
	sortGroups(risk.UnsafeGroups)
	sortGroups(risk.NoProfitGroups)

	candidates := make([]storage.RelayGroup, 0)
	for _, group := range allGroups {
		if isActive(group.Status) && samePlatform(group.Platform, account.Platform) && (!restrictModelCandidates || sameModelType(account, group)) && sameAccountType(account, group, groupTypes) && group.RateMultiplier > cost {
			candidates = append(candidates, group)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RateMultiplier != candidates[j].RateMultiplier {
			return candidates[i].RateMultiplier < candidates[j].RateMultiplier
		}
		return candidates[i].ExternalID < candidates[j].ExternalID
	})
	for _, candidate := range candidates {
		// 可降级是展示和人工操作参考，必须有明确的模型类型绑定，且候选组支持该类型。
		// 风险状态与普通调组建议仍沿用原有逻辑；模型类型只收紧可降级提示和自动调组。
		if strings.TrimSpace(account.ModelType) != "" && sameModelType(account, candidate) && candidate.RateMultiplier < minCurrent && !sameMultiplier(candidate.RateMultiplier, minCurrent) {
			risk.DowngradeGroups = append(risk.DowngradeGroups, candidate)
		}
	}
	if len(risk.DowngradeGroups) > 0 {
		// Keep the lowest-rate candidate for backward compatibility with clients
		// that still consume the original singular field.
		downgrade := risk.DowngradeGroups[0]
		risk.DowngradeGroup = &downgrade
	}
	if len(risk.UnsafeGroups) == 0 && len(risk.NoProfitGroups) == 0 {
		return risk
	}
	if len(candidates) == 0 {
		if len(risk.UnsafeGroups) > 0 {
			risk.State = RiskStateNoSafeCandidate
		} else {
			risk.State = RiskStateNoProfit
		}
		return risk
	}
	recommended := candidates[0]
	risk.RecommendedGroup = &recommended
	needsAdjustment := make(map[int64]struct{}, len(risk.UnsafeGroups)+len(risk.NoProfitGroups))
	for _, group := range risk.UnsafeGroups {
		needsAdjustment[group.ExternalID] = struct{}{}
	}
	for _, group := range risk.NoProfitGroups {
		needsAdjustment[group.ExternalID] = struct{}{}
	}
	profitableCurrent := false
	seen := make(map[int64]struct{}, len(currentGroups)+1)
	for _, group := range currentGroups {
		if _, needs := needsAdjustment[group.ExternalID]; needs {
			continue
		}
		if isActive(group.Status) && samePlatform(group.Platform, account.Platform) && sameAccountType(account, group, groupTypes) && group.RateMultiplier > cost {
			profitableCurrent = true
		}
		if _, exists := seen[group.ExternalID]; !exists {
			risk.SuggestedGroupIDs = append(risk.SuggestedGroupIDs, group.ExternalID)
			seen[group.ExternalID] = struct{}{}
		}
	}
	if !profitableCurrent {
		risk.SuggestedGroupIDs = append(risk.SuggestedGroupIDs, recommended.ExternalID)
	}
	sort.Slice(risk.SuggestedGroupIDs, func(i, j int) bool { return risk.SuggestedGroupIDs[i] < risk.SuggestedGroupIDs[j] })
	if len(risk.UnsafeGroups) > 0 {
		risk.State = RiskStateRisk
	} else {
		risk.State = RiskStateNoProfit
	}
	risk.CanApply = !sameIDs(groupIDs(currentGroups), risk.SuggestedGroupIDs)
	return risk
}

func (s *Service) ApplySuggestion(ctx context.Context, stationID uint, accountExternalID int64) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	risks, err := s.Risks(stationID)
	if err != nil {
		return err
	}
	for _, risk := range risks {
		if risk.Account.ExternalID == accountExternalID {
			if (risk.State != RiskStateRisk && risk.State != RiskStateNoProfit) || !risk.CanApply {
				return errors.New("该账号当前没有可应用的有利润调组建议")
			}
			return s.applyRisk(ctx, station, risk, "manual", "group_update")
		}
	}
	return errors.New("中转站账号不存在或快照已过期")
}

func (s *Service) SetGroups(ctx context.Context, stationID uint, accountExternalID int64, groupExternalIDs []int64) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	accounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return err
	}
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return err
	}
	var account storage.RelayAccount
	for _, item := range accounts {
		if item.ExternalID == accountExternalID {
			account = item
			break
		}
	}
	if account.ExternalID == 0 {
		return errors.New("中转站账号不存在或快照已过期")
	}
	valid := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		valid[group.ExternalID] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(groupExternalIDs))
	selected := make([]int64, 0, len(groupExternalIDs))
	for _, id := range groupExternalIDs {
		if _, ok := valid[id]; !ok {
			return fmt.Errorf("销售分组 %d 不存在", id)
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			selected = append(selected, id)
		}
	}
	links, err := s.stations.ListAccountLinks(stationID)
	if err != nil {
		return err
	}
	byGroup := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		byGroup[group.ID] = group
	}
	var current []storage.RelayGroup
	for _, link := range links {
		if link.RelayAccountID != account.ID {
			continue
		}
		if group, ok := byGroup[link.RelayGroupID]; ok {
			current = append(current, group)
		}
	}
	risk := AccountRisk{Account: account, CurrentGroups: current, SuggestedGroupIDs: selected, CanApply: !sameIDs(groupIDs(current), selected)}
	if !risk.CanApply {
		return nil
	}
	return s.applyRisk(ctx, station, risk, "manual", "group_update")
}

// AddGroup reads the account's current assignments from Sub2API immediately
// before updating them. The quick-add action must never turn a stale dashboard
// snapshot into a destructive full replacement.
func (s *Service) AddGroup(ctx context.Context, stationID uint, accountExternalID, groupExternalID int64) error {
	return s.addGroups(ctx, stationID, accountExternalID, []int64{groupExternalID}, &groupExternalID)
}

// addGroups reads the account's live assignments once and appends every
// requested group in one remote update. This preserves assignments that may
// have changed since the local snapshot and avoids partial multi-group state.
func (s *Service) addGroups(ctx context.Context, stationID uint, accountExternalID int64, groupExternalIDs []int64, recommendedGroupID *int64) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	account, err := s.stations.FindAccountByExternalID(stationID, accountExternalID)
	if err != nil {
		return errors.New("中转站账号不存在或快照已过期")
	}
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return err
	}
	validGroups := make(map[int64]struct{}, len(groups))
	for _, group := range groups {
		validGroups[group.ExternalID] = struct{}{}
	}
	groupExternalIDs = uniqueGroupIDs(groupExternalIDs)
	if len(groupExternalIDs) == 0 {
		return errors.New("至少选择一个销售分组")
	}
	for _, groupID := range groupExternalIDs {
		if _, ok := validGroups[groupID]; !ok {
			return fmt.Errorf("销售分组 %d 不存在", groupID)
		}
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return fmt.Errorf("decrypt admin API key: %w", err)
	}
	remote, err := s.fetchAccount(ctx, station.BaseURL, apiKey, accountExternalID)
	if err != nil {
		return err
	}
	oldIDs := uniqueGroupIDs(remote.GroupIDs)
	newIDs := appendGroupIDs(oldIDs, groupExternalIDs)
	if sameIDs(oldIDs, newIDs) {
		return s.stations.SetAccountGroups(stationID, accountExternalID, oldIDs)
	}
	return s.applyGroupIDs(ctx, station, *account, oldIDs, newIDs, "manual", "group_update", recommendedGroupID)
}

func uniqueGroupIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func appendGroupID(current []int64, groupID int64) []int64 {
	return appendGroupIDs(current, []int64{groupID})
}

func appendGroupIDs(current, additions []int64) []int64 {
	result := uniqueGroupIDs(current)
	seen := make(map[int64]struct{}, len(result)+len(additions))
	for _, id := range result {
		seen[id] = struct{}{}
	}
	for _, id := range uniqueGroupIDs(additions) {
		if _, exists := seen[id]; exists {
			continue
		}
		result = append(result, id)
		seen[id] = struct{}{}
	}
	return result
}

// SetGroupsBatch applies one selected sales-group set to several accounts.
// Each account is audited independently so a single remote failure does not
// hide successful updates for the remaining selections.
func (s *Service) SetGroupsBatch(ctx context.Context, stationID uint, accountExternalIDs, groupExternalIDs []int64) error {
	seen := make(map[int64]struct{}, len(accountExternalIDs))
	var failures []string
	for _, accountID := range accountExternalIDs {
		if accountID <= 0 {
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		if err := s.SetGroups(ctx, stationID, accountID, groupExternalIDs); err != nil {
			failures = append(failures, fmt.Sprintf("账号 %d: %v", accountID, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("批量调整销售分组部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

// SetSchedulable updates the remote account scheduling flag and keeps the local
// snapshot plus adjustment audit log in sync with the remote result.
func (s *Service) SetSchedulable(ctx context.Context, stationID uint, accountExternalID int64, schedulable bool) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	account, err := s.stations.FindAccountByExternalID(stationID, accountExternalID)
	if err != nil {
		return errors.New("中转站账号不存在或快照已过期")
	}
	if account.Schedulable == schedulable {
		return nil
	}
	groups, err := s.accountCurrentGroups(stationID, account.ID)
	if err != nil {
		return err
	}
	return s.setScheduling(ctx, station, *account, groups, schedulable, "manual")
}

// SetSchedulableBatch applies the scheduling state to the current filtered
// account IDs. Each account keeps its own remote update and audit record.
func (s *Service) SetSchedulableBatch(ctx context.Context, stationID uint, accountExternalIDs []int64, schedulable bool) (AccountBatchActionResult, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return AccountBatchActionResult{}, err
	}
	risks, err := s.Risks(stationID)
	if err != nil {
		return AccountBatchActionResult{}, err
	}
	riskByAccount := make(map[int64]AccountRisk, len(risks))
	for _, risk := range risks {
		riskByAccount[risk.Account.ExternalID] = risk
	}

	result := AccountBatchActionResult{}
	for _, accountID := range uniqueAccountIDs(accountExternalIDs) {
		result.Requested++
		risk, ok := riskByAccount[accountID]
		if !ok {
			result.addFailure(accountID, errors.New("账号不存在或快照已过期"))
			continue
		}
		if risk.Account.Schedulable == schedulable {
			result.Skipped++
			continue
		}
		if err := s.setScheduling(ctx, station, risk.Account, risk.CurrentGroups, schedulable, "manual"); err != nil {
			result.addFailure(accountID, err)
			continue
		}
		result.Applied++
	}
	return result, nil
}

// ApplySuggestionsBatch accepts every currently valid regroup recommendation
// in the supplied account set. Accounts without an applicable recommendation
// are reported as skipped instead of failed.
func (s *Service) ApplySuggestionsBatch(ctx context.Context, stationID uint, accountExternalIDs []int64) (AccountBatchActionResult, error) {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return AccountBatchActionResult{}, err
	}
	risks, err := s.Risks(stationID)
	if err != nil {
		return AccountBatchActionResult{}, err
	}
	riskByAccount := make(map[int64]AccountRisk, len(risks))
	for _, risk := range risks {
		riskByAccount[risk.Account.ExternalID] = risk
	}

	result := AccountBatchActionResult{}
	for _, accountID := range uniqueAccountIDs(accountExternalIDs) {
		result.Requested++
		risk, ok := riskByAccount[accountID]
		if !ok {
			result.addFailure(accountID, errors.New("账号不存在或快照已过期"))
			continue
		}
		if (risk.State != RiskStateRisk && risk.State != RiskStateNoProfit) || !risk.CanApply {
			result.Skipped++
			continue
		}
		if err := s.applyRisk(ctx, station, risk, "manual", "group_update"); err != nil {
			result.addFailure(accountID, err)
			continue
		}
		result.Applied++
	}
	return result, nil
}

// AddDowngradesBatch keeps every current remote group assignment and appends
// every safe downgrade group in the account's configured model type.
func (s *Service) AddDowngradesBatch(ctx context.Context, stationID uint, accountExternalIDs []int64) (AccountBatchActionResult, error) {
	if _, err := s.stations.FindByID(stationID); err != nil {
		return AccountBatchActionResult{}, err
	}
	risks, err := s.Risks(stationID)
	if err != nil {
		return AccountBatchActionResult{}, err
	}
	riskByAccount := make(map[int64]AccountRisk, len(risks))
	for _, risk := range risks {
		riskByAccount[risk.Account.ExternalID] = risk
	}

	result := AccountBatchActionResult{}
	for _, accountID := range uniqueAccountIDs(accountExternalIDs) {
		result.Requested++
		risk, ok := riskByAccount[accountID]
		if !ok {
			result.addFailure(accountID, errors.New("账号不存在或快照已过期"))
			continue
		}
		if len(risk.DowngradeGroups) == 0 {
			result.Skipped++
			continue
		}
		if err := s.addGroups(ctx, stationID, accountID, groupIDs(risk.DowngradeGroups), nil); err != nil {
			result.addFailure(accountID, err)
			continue
		}
		result.Applied++
	}
	return result, nil
}

func uniqueAccountIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func (r *AccountBatchActionResult) addFailure(accountID int64, err error) {
	r.Failed++
	r.Errors = append(r.Errors, fmt.Sprintf("账号 %d: %v", accountID, err))
}

// SetRuntimeSettingsBatch updates Sub2API account runtime settings and mirrors
// successful updates into the local snapshot. Only priority changes are audited;
// concurrency and retry count are fully manual controls.
func (s *Service) SetRuntimeSettingsBatch(ctx context.Context, stationID uint, accountExternalIDs []int64, concurrency, priority *int, retryCount ...*int) error {
	return s.setRuntimeSettingsBatch(ctx, stationID, accountExternalIDs, concurrency, priority, "manual", retryCount...)
}

func (s *Service) setRuntimeSettingsBatch(ctx context.Context, stationID uint, accountExternalIDs []int64, concurrency, priority *int, source string, retryCount ...*int) error {
	var poolModeRetryCount *int
	if len(retryCount) > 0 {
		poolModeRetryCount = retryCount[0]
	}
	if concurrency == nil && priority == nil && poolModeRetryCount == nil {
		return errors.New("并发数、优先级和同账号重试次数至少设置一项")
	}
	if concurrency != nil && (*concurrency < 1 || *concurrency > 1000) {
		return errors.New("并发数必须在 1 到 1000 之间")
	}
	if priority != nil && (*priority < 1 || *priority > 1000) {
		return errors.New("优先级必须在 1 到 1000 之间")
	}
	if poolModeRetryCount != nil && (*poolModeRetryCount < 0 || *poolModeRetryCount > 10) {
		return errors.New("同账号重试次数必须在 0 到 10 之间")
	}
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	apiKey, decryptErr := s.cipher.Decrypt(station.APIKeyCipher)
	seen := make(map[int64]struct{}, len(accountExternalIDs))
	failures := make([]string, 0)
	for _, accountID := range accountExternalIDs {
		if accountID <= 0 {
			failures = append(failures, fmt.Sprintf("账号 %d: ID 无效", accountID))
			continue
		}
		if _, ok := seen[accountID]; ok {
			continue
		}
		seen[accountID] = struct{}{}
		account, findErr := s.stations.FindAccountByExternalID(stationID, accountID)
		if findErr != nil {
			failures = append(failures, fmt.Sprintf("账号 %d: 快照不存在", accountID))
			continue
		}
		body := make(map[string]any, 3)
		needsRetryCredentials := false
		if concurrency != nil && account.Concurrency != *concurrency {
			body["concurrency"] = *concurrency
		}
		if priority != nil && account.Priority != *priority {
			body["priority"] = *priority
		}
		if poolModeRetryCount != nil && account.PoolModeRetryCount != *poolModeRetryCount {
			if normalizedAccountType(account.Type) != "apikey" && normalizedAccountType(account.Type) != "bedrock" {
				failures = append(failures, fmt.Sprintf("账号 %s: 同账号重试次数仅支持 API Key 或 Bedrock 账号", account.Name))
				continue
			}
			if !account.PoolMode {
				failures = append(failures, fmt.Sprintf("账号 %s: 未开启池模式，无法设置同账号重试次数", account.Name))
				continue
			}
			needsRetryCredentials = true
		}
		if !hasRuntimeSettingsChanges(body, needsRetryCredentials) {
			continue
		}
		log := runtimeSettingsAdjustmentLog(*account, stationID, priority, source)
		writeLog := func() error {
			if !shouldRecordRuntimeSettingsAdjustment(log) {
				return nil
			}
			return s.stations.CreateAdjustmentLog(log)
		}
		if priority != nil && *priority == 1 && normalizedAccountType(account.Type) != "oauth" {
			err := errors.New("优先级 1 仅保留给 OAuth 账号")
			log.ErrorMessage = err.Error()
			if logErr := writeLog(); logErr != nil {
				failures = append(failures, fmt.Sprintf("账号 %s: %v；审计记录写入失败: %v", account.Name, err, logErr))
			} else {
				failures = append(failures, fmt.Sprintf("账号 %s: %v", account.Name, err))
			}
			continue
		}
		if decryptErr != nil {
			err := fmt.Errorf("decrypt admin API key: %w", decryptErr)
			log.ErrorMessage = err.Error()
			if logErr := writeLog(); logErr != nil {
				failures = append(failures, fmt.Sprintf("账号 %s: %v；审计记录写入失败: %v", account.Name, err, logErr))
			} else {
				failures = append(failures, fmt.Sprintf("账号 %s: %v", account.Name, err))
			}
			continue
		}
		if needsRetryCredentials {
			remote, fetchErr := s.fetchAccount(ctx, station.BaseURL, apiKey, accountID)
			if fetchErr != nil {
				log.ErrorMessage = fmt.Errorf("读取远端账号凭据失败: %w", fetchErr).Error()
				if logErr := writeLog(); logErr != nil {
					failures = append(failures, fmt.Sprintf("账号 %s: %s；审计记录写入失败: %v", account.Name, log.ErrorMessage, logErr))
				} else {
					failures = append(failures, fmt.Sprintf("账号 %s: %s", account.Name, log.ErrorMessage))
				}
				continue
			}
			body["credentials"] = mergePoolModeRetryCredentials(remote.Credentials.Raw, *poolModeRetryCount)
		}
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d", station.BaseURL, accountID)
		if putErr := s.put(ctx, endpoint, apiKey, body); putErr != nil {
			log.ErrorMessage = putErr.Error()
			if logErr := writeLog(); logErr != nil {
				failures = append(failures, fmt.Sprintf("账号 %s: %v；审计记录写入失败: %v", account.Name, putErr, logErr))
			} else {
				failures = append(failures, fmt.Sprintf("账号 %s: %v", account.Name, putErr))
			}
			continue
		}
		if updateErr := s.stations.SetAccountRuntimeSettings(stationID, accountID, concurrency, priority, poolModeRetryCount); updateErr != nil {
			log.ErrorMessage = fmt.Sprintf("远端已更新，本地快照更新失败: %v", updateErr)
			if logErr := writeLog(); logErr != nil {
				failures = append(failures, fmt.Sprintf("账号 %s: %s；审计记录写入失败: %v", account.Name, log.ErrorMessage, logErr))
			} else {
				failures = append(failures, fmt.Sprintf("账号 %s: %s", account.Name, log.ErrorMessage))
			}
			continue
		}
		log.Success = true
		if logErr := writeLog(); logErr != nil {
			failures = append(failures, fmt.Sprintf("账号 %s: 参数已更新，审计记录写入失败: %v", account.Name, logErr))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("批量设置账号参数部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

func hasRuntimeSettingsChanges(body map[string]any, needsRetryCredentials bool) bool {
	return len(body) > 0 || needsRetryCredentials
}

func mergePoolModeRetryCredentials(raw map[string]any, retryCount int) map[string]any {
	credentials := make(map[string]any, len(raw)+2)
	for key, value := range raw {
		credentials[key] = value
	}
	credentials["pool_mode"] = true
	credentials["pool_mode_retry_count"] = retryCount
	return credentials
}

func runtimeSettingsAdjustmentLog(account storage.RelayAccount, stationID uint, priority *int, source string) *storage.RelayAccountAdjustmentLog {
	emptyGroups := "[]"
	log := &storage.RelayAccountAdjustmentLog{
		RelayStationID: stationID, RelayAccountExternalID: account.ExternalID,
		AccountName: account.Name, AccountPlatform: account.Platform,
		CostMultiplier: costMultiplier(account), OldGroupIDsJSON: emptyGroups,
		NewGroupIDsJSON: emptyGroups, Source: source, AppliedAt: time.Now().UTC(),
	}
	if priority != nil && account.Priority != *priority {
		oldValue, newValue := account.Priority, *priority
		log.OldPriority, log.NewPriority = &oldValue, &newValue
	}
	log.Action = "priority_update"
	return log
}

func shouldRecordRuntimeSettingsAdjustment(log *storage.RelayAccountAdjustmentLog) bool {
	return log != nil && log.OldPriority != nil && log.NewPriority != nil
}

func latencySmoothnessScore(account storage.RelayAccount) (float64, bool) {
	var samples []storage.RelayLatencySample
	if account.LatencySamplesJSON == "" || json.Unmarshal([]byte(account.LatencySamplesJSON), &samples) != nil || len(samples) == 0 {
		return 0, false
	}
	total := 0.0
	for _, sample := range samples {
		total += float64(sample.FirstTokenMS)/10_000 + float64(sample.DurationMS)/60_000
	}
	return total / float64(len(samples)), true
}

func automaticPriorityTargets(accounts []storage.RelayAccount) map[int64]int {
	targets := make(map[int64]int, len(accounts))
	type scoredAccount struct {
		id    int64
		score float64
	}
	scored := make([]scoredAccount, 0, len(accounts))
	for _, account := range accounts {
		switch normalizedAccountType(account.Type) {
		case "oauth":
			targets[account.ExternalID] = 1
		case "apikey":
			score, ok := latencySmoothnessScore(account)
			if !ok {
				targets[account.ExternalID] = 2
				continue
			}
			scored = append(scored, scoredAccount{id: account.ExternalID, score: score})
		}
	}
	sort.Slice(scored, func(i, j int) bool {
		if !sameMultiplier(scored[i].score, scored[j].score) {
			return scored[i].score < scored[j].score
		}
		return scored[i].id < scored[j].id
	})
	uniqueScores := make([]float64, 0, len(scored))
	for _, item := range scored {
		if len(uniqueScores) == 0 || !sameMultiplier(uniqueScores[len(uniqueScores)-1], item.score) {
			uniqueScores = append(uniqueScores, item.score)
		}
	}
	for _, item := range scored {
		level := sort.Search(len(uniqueScores), func(index int) bool {
			return uniqueScores[index] >= item.score || sameMultiplier(uniqueScores[index], item.score)
		})
		priority := 2
		if len(uniqueScores) > 1 {
			priority += (level*8 + (len(uniqueScores)-1)/2) / (len(uniqueScores) - 1)
		}
		targets[item.id] = priority
	}
	return targets
}

func automaticPriorityTargetsWithRecall(accounts []storage.RelayAccount, recallEnabled bool, recallMinutes int, now time.Time) map[int64]int {
	targets := automaticPriorityTargets(accounts)
	if !recallEnabled || recallMinutes <= 0 {
		return targets
	}
	threshold := now.Add(-time.Duration(recallMinutes) * time.Minute)
	for _, account := range accounts {
		if normalizedAccountType(account.Type) != "apikey" || !account.Schedulable {
			continue
		}
		if account.LastUsedAt == nil || account.LastUsedAt.Before(threshold) {
			// Recall is deliberately independent of the latency baseline: an idle
			// API key should be promoted one level per snapshot until priority 2.
			// Once the account is used again, the next snapshot restores the
			// latency-based target from automaticPriorityTargets.
			if account.Priority <= 2 {
				targets[account.ExternalID] = 2
			} else {
				targets[account.ExternalID] = account.Priority - 1
			}
		}
	}
	return targets
}

func (s *Service) applyAutomaticPriorities(ctx context.Context, station *storage.RelayStation) error {
	accounts, err := s.stations.ListAccounts(station.ID)
	if err != nil {
		return err
	}
	targets := automaticPriorityTargetsWithRecall(accounts, station.AutoPriorityRecallEnabled, station.AutoPriorityRecallMinutes, time.Now())
	failures := make([]string, 0)
	for _, account := range accounts {
		target, ok := targets[account.ExternalID]
		if !ok || account.Priority == target {
			continue
		}
		if err := s.setRuntimeSettingsBatch(ctx, station.ID, []int64{account.ExternalID}, nil, &target, "auto"); err != nil {
			failures = append(failures, fmt.Sprintf("账号 %s: %v", account.Name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("自动优先级调整部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (s *Service) accountCurrentGroups(stationID uint, accountID uint) ([]storage.RelayGroup, error) {
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return nil, err
	}
	links, err := s.stations.ListAccountLinks(stationID)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	current := make([]storage.RelayGroup, 0)
	for _, link := range links {
		if link.RelayAccountID == accountID {
			if group, ok := byID[link.RelayGroupID]; ok {
				current = append(current, group)
			}
		}
	}
	sortGroups(current)
	return current, nil
}

func (s *Service) applyAutomaticAdjustments(ctx context.Context, station *storage.RelayStation) error {
	risks, err := s.Risks(station.ID)
	if err != nil {
		return err
	}
	allGroups, err := s.stations.ListGroups(station.ID)
	if err != nil {
		return err
	}
	groupTypes := make(map[uint]map[string]struct{})
	for _, item := range risks {
		for _, group := range item.CurrentGroups {
			if groupTypes[group.ID] == nil {
				groupTypes[group.ID] = make(map[string]struct{})
			}
			groupTypes[group.ID][normalizedAccountType(item.Account.Type)] = struct{}{}
		}
	}
	var failures []string
	for _, risk := range risks {
		// 模型类型只约束自动调组候选。未绑定类型的账号仍保留原销售分组和风险判定，
		// 但不会由自动策略改组或关闭调度。
		if strings.TrimSpace(risk.Account.ModelType) == "" {
			continue
		}
		// 风险展示沿用销售分组原规则；只有自动执行时重新按绑定模型筛选候选。
		risk = evaluateAccountRiskWithOptions(risk.Account, risk.CurrentGroups, allGroups, groupTypes, true)
		switch risk.State {
		case RiskStateRisk:
			if !station.AutoAdjustEnabled || !risk.CanApply {
				continue
			}
			if err := s.applyRisk(ctx, station, risk, "auto", "group_update"); err != nil {
				failures = append(failures, fmt.Sprintf("账号 %s 调组: %v", risk.Account.Name, err))
			}
		case RiskStateNoProfit:
			if !station.AutoAdjustNoProfitEnabled || !risk.CanApply {
				continue
			}
			if err := s.applyRisk(ctx, station, risk, "auto", "group_update"); err != nil {
				failures = append(failures, fmt.Sprintf("账号 %s 调整无利润分组: %v", risk.Account.Name, err))
			}
		case RiskStateNoSafeCandidate:
			if !station.AutoAdjustEnabled || !risk.Account.Schedulable {
				continue
			}
			if err := s.disableScheduling(ctx, station, risk); err != nil {
				failures = append(failures, fmt.Sprintf("账号 %s 关闭调度: %v", risk.Account.Name, err))
			}
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("自动调度处理部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (s *Service) applyRisk(ctx context.Context, station *storage.RelayStation, risk AccountRisk, source, action string) error {
	oldIDs := groupIDs(risk.CurrentGroups)
	var recommendedGroupID *int64
	if risk.RecommendedGroup != nil {
		id := risk.RecommendedGroup.ExternalID
		recommendedGroupID = &id
	}
	return s.applyGroupIDs(ctx, station, risk.Account, oldIDs, risk.SuggestedGroupIDs, source, action, recommendedGroupID)
}

func (s *Service) applyGroupIDs(ctx context.Context, station *storage.RelayStation, account storage.RelayAccount, oldIDs, newIDs []int64, source, action string, recommendedGroupID *int64) error {
	newJSON, _ := json.Marshal(newIDs)
	oldJSON, _ := json.Marshal(oldIDs)
	log := &storage.RelayAccountAdjustmentLog{
		RelayStationID: station.ID, RelayAccountExternalID: account.ExternalID,
		AccountName: account.Name, AccountPlatform: account.Platform,
		CostMultiplier: costMultiplier(account), OldGroupIDsJSON: string(oldJSON),
		NewGroupIDsJSON: string(newJSON), Source: source, Action: action, AppliedAt: time.Now().UTC(),
	}
	if recommendedGroupID != nil {
		id := *recommendedGroupID
		log.RecommendedGroupID = &id
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err == nil {
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d", station.BaseURL, account.ExternalID)
		err = s.put(ctx, endpoint, apiKey, map[string]any{"group_ids": newIDs})
	}
	if err != nil {
		log.ErrorMessage = err.Error()
		_ = s.stations.CreateAdjustmentLog(log)
		return err
	}
	if err := s.stations.SetAccountGroups(station.ID, account.ExternalID, newIDs); err != nil {
		log.ErrorMessage = fmt.Sprintf("远端已更新，本地快照更新失败: %v", err)
		_ = s.stations.CreateAdjustmentLog(log)
		return err
	}
	log.Success = true
	return s.stations.CreateAdjustmentLog(log)
}

func (s *Service) disableScheduling(ctx context.Context, station *storage.RelayStation, risk AccountRisk) error {
	return s.setScheduling(ctx, station, risk.Account, risk.CurrentGroups, false, "auto")
}

func (s *Service) setScheduling(ctx context.Context, station *storage.RelayStation, account storage.RelayAccount, groups []storage.RelayGroup, schedulable bool, source string) error {
	oldIDs := groupIDs(groups)
	encoded, _ := json.Marshal(oldIDs)
	action := "disable_scheduling"
	if schedulable {
		action = "enable_scheduling"
	}
	log := &storage.RelayAccountAdjustmentLog{
		RelayStationID: station.ID, RelayAccountExternalID: account.ExternalID,
		AccountName: account.Name, AccountPlatform: account.Platform,
		CostMultiplier: costMultiplier(account), OldGroupIDsJSON: string(encoded),
		NewGroupIDsJSON: string(encoded), Source: source, Action: action, AppliedAt: time.Now().UTC(),
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err == nil {
		endpoint := fmt.Sprintf("%s/api/v1/admin/accounts/%d/schedulable", station.BaseURL, account.ExternalID)
		err = s.post(ctx, endpoint, apiKey, map[string]any{"schedulable": schedulable}, nil)
	}
	if err != nil {
		log.ErrorMessage = err.Error()
		_ = s.stations.CreateAdjustmentLog(log)
		return err
	}
	if err := s.stations.SetAccountSchedulable(station.ID, account.ExternalID, schedulable); err != nil {
		log.ErrorMessage = fmt.Sprintf("远端已更新，本地快照更新失败: %v", err)
		_ = s.stations.CreateAdjustmentLog(log)
		return err
	}
	log.Success = true
	return s.stations.CreateAdjustmentLog(log)
}

func groupIDs(groups []storage.RelayGroup) []int64 {
	ids := make([]int64, 0, len(groups))
	for _, group := range groups {
		ids = append(ids, group.ExternalID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]int64(nil), a...)
	b = append([]int64(nil), b...)
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortGroups(groups []storage.RelayGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Name != groups[j].Name {
			return groups[i].Name < groups[j].Name
		}
		return groups[i].ExternalID < groups[j].ExternalID
	})
}

func samePlatform(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func normalizedAccountType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	return value
}

func sameAccountType(account storage.RelayAccount, group storage.RelayGroup, groupTypes map[uint]map[string]struct{}) bool {
	if group.RequireOAuthOnly && strings.EqualFold(strings.TrimSpace(account.Type), "apikey") {
		return false
	}
	if types := groupTypes[group.ID]; len(types) > 0 {
		_, ok := types[normalizedAccountType(account.Type)]
		return ok
	}
	// 空分组没有历史类型约束；RequireOAuthOnly 仍然是明确的硬限制。
	return true
}

func sameModelType(account storage.RelayAccount, group storage.RelayGroup) bool {
	var configured []string
	if strings.TrimSpace(group.ModelTypesJSON) != "" {
		_ = json.Unmarshal([]byte(group.ModelTypesJSON), &configured)
	}
	if len(configured) == 0 {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(account.ModelType))
	if value == "" {
		return false
	}
	for _, candidate := range configured {
		if strings.EqualFold(strings.TrimSpace(candidate), value) {
			return true
		}
	}
	return false
}

func isActive(status string) bool { return strings.EqualFold(strings.TrimSpace(status), "active") }

func sameMultiplier(a, b float64) bool {
	const epsilon = 1e-9
	return a-b <= epsilon && b-a <= epsilon
}

func costMultiplier(account storage.RelayAccount) float64 {
	if account.RateMultiplier == nil {
		return 0
	}
	return *account.RateMultiplier
}
