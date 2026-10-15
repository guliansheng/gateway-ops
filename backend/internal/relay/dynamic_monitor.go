package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// 账号动态监测：按“首字延迟连续超标”关闭参与监测的账号，再按“分组最少保留账号数”
// 和“超时自动开启”把之前被规则关闭的账号开回来。
//
// 规则与运行状态都以中转站为单位保存（relay_stations.dynamic_monitor_config /
// dynamic_monitor_state），只对当前功能生效：不写分组字段，也不改自动调组策略。
// 关闭判断在快照同步末尾执行（此时首字延迟样本刚刚刷新），超时开启由每分钟的
// 独立任务处理。

// sourceDynamicMonitor 写入调整审计日志的来源，用于区分手动与自动调组。
const sourceDynamicMonitor = "dynamic"

// dynamicMonitorReasonFirstToken 是关闭原因：首字延迟连续超过设定阈值。
const dynamicMonitorReasonFirstToken = "first_token"

// dynamicMonitorReasonSingleFirstToken 是关闭原因：单次首字延迟超过设定阈值。
const dynamicMonitorReasonSingleFirstToken = "first_token_single"

const (
	// 阈值按秒配置（比毫秒更贴近实际调参直觉），内部仍换算成毫秒与样本比对。
	dynamicMonitorMinThresholdSeconds = 0.1
	dynamicMonitorMaxThresholdSeconds = 600 // 10 分钟
	dynamicMonitorMaxConsecutive      = 100
	dynamicMonitorMaxMinAccounts      = 1000
	dynamicMonitorMaxAutoOpen         = 10080 // 7 天
	dynamicMonitorMaxAccounts         = 1000
	dynamicMonitorMaxGroups           = 20

	dynamicMonitorDefaultThresholdSeconds = 10
	dynamicMonitorDefaultConsecutive      = 3
)

// DynamicMonitorRuleGroup 一组独立规则：自己的首字延迟阈值（秒）、超标次数、
// 单次超时阈值与参与账号。同一个账号最多归属一个规则组，判定时按其所在组的规则评估。
// 两条关闭规则并存：任意一条命中即关闭账号。
type DynamicMonitorRuleGroup struct {
	// Name 规则组名称，仅用于界面展示。
	Name string `json:"name"`
	// FirstTokenSeconds 首字延迟超过该秒数记为一次超标。
	FirstTokenSeconds float64 `json:"first_token_seconds"`
	// ConsecutiveCount 连续超标多少次后关闭账号。
	ConsecutiveCount int `json:"consecutive_count"`
	// SingleTimeoutSeconds 单次首字延迟超过该秒数时直接关闭账号，0 表示不启用这条规则。
	SingleTimeoutSeconds float64 `json:"single_timeout_seconds"`
	// AccountExternalIDs 该规则组参与监测的账号。
	AccountExternalIDs []int64 `json:"account_external_ids"`
}

// DynamicMonitorConfig 是弹窗里保存的完整规则。所有字段只属于该中转站的这个功能。
// 阈值与超标次数按规则组各自设置；补开相关的三项是整个中转站通用设置。
type DynamicMonitorConfig struct {
	Enabled bool `json:"enabled"`
	// Groups 多组规则，每组独立的阈值与超标次数。
	Groups []DynamicMonitorRuleGroup `json:"groups"`
	// MinAccountsPerGroup 通用规则：分组至少保留多少个可用账号，0 表示不设置。
	// 未在 GroupMinAccounts 中单独设置的分组都使用这个公共值。
	MinAccountsPerGroup int `json:"min_accounts_per_group"`
	// GroupMinAccounts 按销售分组（external id 的十进制字符串）单独设置的最少保留账号数。
	// 出现即覆盖 MinAccountsPerGroup；未出现的分组沿用公共值。值为 0 表示该分组不设下限。
	GroupMinAccounts map[string]int `json:"group_min_accounts,omitempty"`
	// AllowAutoOpen 通用规则：关闭的账号是否允许被动态开启。
	AllowAutoOpen bool `json:"allow_auto_open"`
	// AutoOpenMinutes 通用规则：关闭多久后自动开启，0 表示不启用超时开启。
	AutoOpenMinutes int `json:"auto_open_minutes"`
}

// GroupMinFloor 返回指定销售分组实际生效的最少保留账号数：
// 单独设置过就用设置值，否则回落到公共值。
func (c DynamicMonitorConfig) GroupMinFloor(groupExternalID int64) int {
	if value, ok := c.GroupMinAccounts[strconv.FormatInt(groupExternalID, 10)]; ok {
		return value
	}
	return c.MinAccountsPerGroup
}

// anyGroupMinFloor 判断是否存在生效下限大于 0 的分组，用于决定是否需要计算补开。
func (c DynamicMonitorConfig) anyGroupMinFloor() bool {
	if c.MinAccountsPerGroup > 0 {
		return true
	}
	for _, value := range c.GroupMinAccounts {
		if value > 0 {
			return true
		}
	}
	return false
}

// DynamicMonitorClosure 记录一个被规则关闭、等待按规则开启的账号。
type DynamicMonitorClosure struct {
	AccountExternalID int64     `json:"account_external_id"`
	AccountName       string    `json:"account_name"`
	Reason            string    `json:"reason"`
	ClosedAt          time.Time `json:"closed_at"`
}

// DynamicMonitorState 是运行状态：只有这里记录的账号才会被自动开启。
type DynamicMonitorState struct {
	ClosedAccounts []DynamicMonitorClosure `json:"closed_accounts"`
	// Judged 是关闭判定的采样下界（不含）：关闭成功时消费本轮样本，
	// 重开成功时推进到开启时间。只有下界之后的新调用参与连续超标计数，
	// 不把重开前的旧样本拼进新一轮。普通快照不会推进下界，以便跨同步累计。
	// 持久化保留到账号删除，兼容没有该字段的旧运行状态。
	Judged map[int64]time.Time `json:"judged,omitempty"`
}

func (state *DynamicMonitorState) advanceJudged(externalID int64, at time.Time) {
	if !at.After(state.Judged[externalID]) {
		return
	}
	if state.Judged == nil {
		state.Judged = make(map[int64]time.Time)
	}
	state.Judged[externalID] = at.UTC()
}

// retainedDynamicMonitorState 不共享输入 map，避免计划计算改写待比较的旧状态。
func retainedDynamicMonitorState(state DynamicMonitorState, accounts []storage.RelayAccount) DynamicMonitorState {
	next := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{}}
	for _, account := range accounts {
		next.advanceJudged(account.ExternalID, state.Judged[account.ExternalID])
	}
	return next
}

func dynamicMonitorSamplesAfter(raw string, after time.Time) []storage.RelayLatencySample {
	samples := parseLatencySamples(raw)
	fresh := samples[:0]
	for _, sample := range samples {
		if sample.CreatedAt.After(after) {
			fresh = append(fresh, sample)
		}
	}
	return fresh
}

func (state *DynamicMonitorState) consumeSamples(account storage.RelayAccount, at time.Time) {
	state.advanceJudged(account.ExternalID, at)
	for _, sample := range parseLatencySamples(account.LatencySamplesJSON) {
		state.advanceJudged(account.ExternalID, sample.CreatedAt)
	}
}

// dynamicMonitorLegacyConfig 用于读取旧版单套规则结构，只在迁移时用到。
type dynamicMonitorLegacyConfig struct {
	AccountExternalIDs    []int64 `json:"account_external_ids"`
	FirstTokenThresholdMS int64   `json:"first_token_threshold_ms"`
	ConsecutiveCount      int     `json:"consecutive_count"`
}

// ParseDynamicMonitorConfig 读取落库的规则，空值返回零值配置。
// 兼容旧版“单套阈值+单份账号”结构：迁移成一个默认规则组，保证已启用的配置继续生效。
// 切片字段统一返回非 nil，避免 JSON 里出现 null 让前端取 length 时报错。
func ParseDynamicMonitorConfig(raw string) DynamicMonitorConfig {
	var config DynamicMonitorConfig
	var legacy dynamicMonitorLegacyConfig
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &config)
		_ = json.Unmarshal([]byte(raw), &legacy)
	}
	if len(config.Groups) == 0 && len(legacy.AccountExternalIDs) > 0 {
		config.Groups = []DynamicMonitorRuleGroup{{
			Name:               "默认规则",
			FirstTokenSeconds:  float64(legacy.FirstTokenThresholdMS) / 1000,
			ConsecutiveCount:   legacy.ConsecutiveCount,
			AccountExternalIDs: legacy.AccountExternalIDs,
		}}
	}
	if config.Groups == nil {
		config.Groups = []DynamicMonitorRuleGroup{}
	}
	return config.Normalize()
}

// ParseDynamicMonitorState 读取落库的运行状态。
func ParseDynamicMonitorState(raw string) DynamicMonitorState {
	var state DynamicMonitorState
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &state)
	}
	if state.ClosedAccounts == nil {
		state.ClosedAccounts = []DynamicMonitorClosure{}
	}
	return state
}

// Normalize 补齐默认值，保证关闭状态下保存的规则同样合法。
// 同时收敛“同一账号出现在多个规则组”的情况：一个账号只归属第一个提到它的组，
// 否则同一账号会被两套阈值同时判定，行为不可预期。
func (c DynamicMonitorConfig) Normalize() DynamicMonitorConfig {
	if c.MinAccountsPerGroup < 0 {
		c.MinAccountsPerGroup = 0
	}
	if c.AutoOpenMinutes < 0 {
		c.AutoOpenMinutes = 0
	}
	// 分组覆盖不在 Normalize 里清洗：Validate 会直接拒绝越界值与非法键，
	// 避免调用方以为设置生效、实际被静默丢弃。
	if len(c.GroupMinAccounts) == 0 {
		c.GroupMinAccounts = nil
	}
	claimed := make(map[int64]struct{})
	groups := make([]DynamicMonitorRuleGroup, 0, len(c.Groups))
	for _, group := range c.Groups {
		normalized := group.Normalize()
		ids := make([]int64, 0, len(normalized.AccountExternalIDs))
		for _, id := range normalized.AccountExternalIDs {
			if _, taken := claimed[id]; taken {
				continue
			}
			claimed[id] = struct{}{}
			ids = append(ids, id)
		}
		normalized.AccountExternalIDs = ids
		if len(ids) == 0 {
			// 没有参与账号的规则组不保留，避免界面出现空壳分组。
			continue
		}
		groups = append(groups, normalized)
	}
	c.Groups = groups
	return c
}

// Normalize 补齐规则组内字段的默认值并去重排序账号。
func (g DynamicMonitorRuleGroup) Normalize() DynamicMonitorRuleGroup {
	g.Name = strings.TrimSpace(g.Name)
	if g.FirstTokenSeconds <= 0 {
		g.FirstTokenSeconds = dynamicMonitorDefaultThresholdSeconds
	}
	if g.ConsecutiveCount <= 0 {
		g.ConsecutiveCount = dynamicMonitorDefaultConsecutive
	}
	if g.SingleTimeoutSeconds < 0 {
		g.SingleTimeoutSeconds = 0
	}
	ids := make([]int64, 0, len(g.AccountExternalIDs))
	seen := make(map[int64]struct{}, len(g.AccountExternalIDs))
	for _, id := range g.AccountExternalIDs {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	g.AccountExternalIDs = ids
	return g
}

// Label 返回规则组在错误提示里用的称呼。
func (g DynamicMonitorRuleGroup) Label(index int) string {
	if g.Name != "" {
		return g.Name
	}
	return fmt.Sprintf("第 %d 组", index+1)
}

// thresholdMS 把秒换算成毫秒，与样本里的首字延迟同单位。
func (g DynamicMonitorRuleGroup) thresholdMS() int64 {
	return int64(g.FirstTokenSeconds * 1000)
}

// singleTimeoutMS 把单次超时阈值换算成毫秒，0 表示这条规则未启用。
func (g DynamicMonitorRuleGroup) singleTimeoutMS() int64 {
	return int64(g.SingleTimeoutSeconds * 1000)
}

// Validate 校验弹窗提交的规则。
func (c DynamicMonitorConfig) Validate() error {
	if len(c.Groups) > dynamicMonitorMaxGroups {
		return fmt.Errorf("规则组不能超过 %d 个", dynamicMonitorMaxGroups)
	}
	totalAccounts := 0
	for index, group := range c.Groups {
		label := group.Label(index)
		if group.FirstTokenSeconds < dynamicMonitorMinThresholdSeconds || group.FirstTokenSeconds > dynamicMonitorMaxThresholdSeconds {
			return fmt.Errorf("%s 首字延迟阈值必须在 %g 到 %g 秒之间", label, dynamicMonitorMinThresholdSeconds, float64(dynamicMonitorMaxThresholdSeconds))
		}
		if group.ConsecutiveCount < 1 || group.ConsecutiveCount > dynamicMonitorMaxConsecutive {
			return fmt.Errorf("%s 连续超标次数必须在 1 到 %d 之间", label, dynamicMonitorMaxConsecutive)
		}
		// 单次超时为 0 表示不启用，启用时与连续阈值使用同一取值范围。
		if group.SingleTimeoutSeconds != 0 && (group.SingleTimeoutSeconds < dynamicMonitorMinThresholdSeconds || group.SingleTimeoutSeconds > dynamicMonitorMaxThresholdSeconds) {
			return fmt.Errorf("%s 单次首字超时必须为 0（不启用）或在 %g 到 %g 秒之间", label, dynamicMonitorMinThresholdSeconds, float64(dynamicMonitorMaxThresholdSeconds))
		}
		if len(group.AccountExternalIDs) == 0 {
			return fmt.Errorf("%s 请至少选择一个参与账号", label)
		}
		totalAccounts += len(group.AccountExternalIDs)
	}
	if totalAccounts > dynamicMonitorMaxAccounts {
		return fmt.Errorf("参与动态监测的账号不能超过 %d 个", dynamicMonitorMaxAccounts)
	}
	if c.MinAccountsPerGroup < 0 || c.MinAccountsPerGroup > dynamicMonitorMaxMinAccounts {
		return fmt.Errorf("分组最少保留账号数必须在 0 到 %d 之间", dynamicMonitorMaxMinAccounts)
	}
	for externalID, value := range c.GroupMinAccounts {
		id, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("分组最少保留账号数的分组 ID %q 不合法", externalID)
		}
		if value < 0 || value > dynamicMonitorMaxMinAccounts {
			return fmt.Errorf("分组 %d 的最少保留账号数必须在 0 到 %d 之间", id, dynamicMonitorMaxMinAccounts)
		}
	}
	if c.AutoOpenMinutes < 0 || c.AutoOpenMinutes > dynamicMonitorMaxAutoOpen {
		return fmt.Errorf("自动开启时间必须在 0 到 %d 分钟之间", dynamicMonitorMaxAutoOpen)
	}
	if c.Enabled && len(c.Groups) == 0 {
		return errors.New("启用监测时请至少配置一个规则组")
	}
	if c.Enabled && c.AllowAutoOpen && c.AutoOpenMinutes <= 0 && !c.anyGroupMinFloor() {
		return errors.New("允许动态开启时，请设置自动开启时间或分组最少保留账号数")
	}
	return nil
}

// anyFirstTokenOver 判断样本里是否存在任意一条首字延迟超过阈值，用于单次超时直接关闭。
func anyFirstTokenOver(samples []storage.RelayLatencySample, thresholdMS int64) bool {
	if thresholdMS <= 0 {
		return false
	}
	for _, sample := range samples {
		if sample.FirstTokenMS > thresholdMS {
			return true
		}
	}
	return false
}

// consecutiveFirstTokenOver 统计最新样本里从头开始连续超过阈值的条数。
// 样本按时间倒序排列，遇到第一条未超标的样本就停止。
func consecutiveFirstTokenOver(samples []storage.RelayLatencySample, thresholdMS int64) int {
	if thresholdMS <= 0 || len(samples) == 0 {
		return 0
	}
	ordered := make([]storage.RelayLatencySample, len(samples))
	copy(ordered, samples)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
	})
	count := 0
	for _, sample := range ordered {
		if sample.FirstTokenMS <= thresholdMS {
			break
		}
		count++
	}
	return count
}

func parseLatencySamples(raw string) []storage.RelayLatencySample {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var samples []storage.RelayLatencySample
	if err := json.Unmarshal([]byte(raw), &samples); err != nil {
		return nil
	}
	return samples
}

func relayAccountSchedulable(account storage.RelayAccount) bool {
	return account.Schedulable && isActive(account.Status)
}

// dynamicMonitorPlan 是一次纯计算结果，便于单独测试。
type dynamicMonitorPlan struct {
	// Close 本轮应关闭的账号（external id）。
	Close []int64
	// CloseReasons 记录每个待关闭账号命中的原因，写入关闭记录用于界面区分。
	CloseReasons map[int64]string
	// Reopen 本轮应开启的账号（external id），只来自之前被规则关闭的账号。
	Reopen []int64
	// State 是清理过期关闭记录后的状态，执行成功后再补上本轮关闭记录。
	State DynamicMonitorState
}

// planDynamicMonitor 计算关闭与补开名单。输入全部来自最近一次快照，不触发任何写操作。
func planDynamicMonitor(
	config DynamicMonitorConfig,
	state DynamicMonitorState,
	accounts []storage.RelayAccount,
	groups []storage.RelayGroup,
	links []storage.RelayAccountGroup,
) dynamicMonitorPlan {
	plan := dynamicMonitorPlan{
		State:        retainedDynamicMonitorState(state, accounts),
		CloseReasons: make(map[int64]string),
	}

	accountByExternalID := make(map[int64]storage.RelayAccount, len(accounts))
	accountByLocalID := make(map[uint]storage.RelayAccount, len(accounts))
	for _, account := range accounts {
		accountByExternalID[account.ExternalID] = account
		accountByLocalID[account.ID] = account
	}

	// 1) 关闭记录只保留仍存在且仍处于关闭状态的账号：账号被删除或已被手动开启时停止跟踪。
	closed := make(map[int64]DynamicMonitorClosure, len(state.ClosedAccounts))
	for _, closure := range state.ClosedAccounts {
		account, ok := accountByExternalID[closure.AccountExternalID]
		if !ok {
			continue
		}
		// 旧版状态没有采样下界，先以关闭时间兜底。
		plan.State.advanceJudged(account.ExternalID, closure.ClosedAt)
		if account.Schedulable {
			// 已由外部/手动重开，无法得知精确时间，从本次观察开始重新采样。
			plan.State.consumeSamples(account, time.Now().UTC())
			continue
		}
		if _, exists := closed[closure.AccountExternalID]; exists {
			continue
		}
		closed[closure.AccountExternalID] = closure
		plan.State.ClosedAccounts = append(plan.State.ClosedAccounts, closure)
	}
	if !config.Enabled {
		return plan
	}

	// 2) 关闭参与监测的账号：连续超标或单次超时，命中任意一条即关闭。
	// 每个账号只按它所属规则组的两条规则判定；账号归属在 Normalize 时已保证唯一。
	ruleByAccount := make(map[int64]DynamicMonitorRuleGroup)
	for _, group := range config.Groups {
		for _, externalID := range group.AccountExternalIDs {
			ruleByAccount[externalID] = group
		}
	}

	for _, account := range accounts {
		rule, ok := ruleByAccount[account.ExternalID]
		if !ok {
			continue
		}
		if !relayAccountSchedulable(account) {
			continue
		}
		if _, alreadyClosed := closed[account.ExternalID]; alreadyClosed {
			continue
		}
		samples := dynamicMonitorSamplesAfter(account.LatencySamplesJSON, plan.State.Judged[account.ExternalID])
		// 单次超时优先级更高：只要有一条新样本超过它就直接关闭。
		if anyFirstTokenOver(samples, rule.singleTimeoutMS()) {
			plan.Close = append(plan.Close, account.ExternalID)
			plan.CloseReasons[account.ExternalID] = dynamicMonitorReasonSingleFirstToken
			continue
		}
		if consecutiveFirstTokenOver(samples, rule.thresholdMS()) >= rule.ConsecutiveCount {
			plan.Close = append(plan.Close, account.ExternalID)
			plan.CloseReasons[account.ExternalID] = dynamicMonitorReasonFirstToken
		}
	}
	sort.Slice(plan.Close, func(i, j int) bool { return plan.Close[i] < plan.Close[j] })

	if !config.AllowAutoOpen || !config.anyGroupMinFloor() || len(closed) == 0 {
		return plan
	}

	// 3) 分组最少保留账号数不足 → 开启之前被规则关闭的账号（关闭时间早的优先）。
	groupsByLocalID := make(map[uint]storage.RelayGroup, len(groups))
	membersByGroupID := make(map[uint][]storage.RelayAccount, len(groups))
	for _, group := range groups {
		groupsByLocalID[group.ID] = group
	}
	for _, link := range links {
		account, ok := accountByLocalID[link.RelayAccountID]
		if !ok {
			continue
		}
		membersByGroupID[link.RelayGroupID] = append(membersByGroupID[link.RelayGroupID], account)
	}
	plannedClose := make(map[int64]struct{}, len(plan.Close))
	for _, externalID := range plan.Close {
		plannedClose[externalID] = struct{}{}
	}
	plannedReopen := make(map[int64]struct{})

	for _, group := range groups {
		if !isActive(group.Status) {
			continue
		}
		// 该销售分组实际生效的下限：单独设置过就用设置值，否则用公共值。
		floor := config.GroupMinFloor(group.ExternalID)
		if floor <= 0 {
			continue
		}
		count := 0
		for _, account := range membersByGroupID[group.ID] {
			if !relayAccountSchedulable(account) {
				continue
			}
			if _, closing := plannedClose[account.ExternalID]; closing {
				continue
			}
			count++
		}
		if count >= floor {
			continue
		}
		candidates := make([]DynamicMonitorClosure, 0)
		for _, account := range membersByGroupID[group.ID] {
			if closure, ok := closed[account.ExternalID]; ok {
				candidates = append(candidates, closure)
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			if !candidates[i].ClosedAt.Equal(candidates[j].ClosedAt) {
				return candidates[i].ClosedAt.Before(candidates[j].ClosedAt)
			}
			return candidates[i].AccountExternalID < candidates[j].AccountExternalID
		})
		for _, candidate := range candidates {
			if count >= floor {
				break
			}
			if _, done := plannedReopen[candidate.AccountExternalID]; done {
				continue
			}
			plannedReopen[candidate.AccountExternalID] = struct{}{}
			plan.Reopen = append(plan.Reopen, candidate.AccountExternalID)
			count++
		}
	}
	sort.Slice(plan.Reopen, func(i, j int) bool { return plan.Reopen[i] < plan.Reopen[j] })
	return plan
}

// SaveDynamicMonitorConfig 保存当前中转站的动态监测规则。
func (s *Service) SaveDynamicMonitorConfig(stationID uint, config DynamicMonitorConfig) (DynamicMonitorConfig, error) {
	// Normalize 只做形状规范化，不清洗分组覆盖：Validate 会直接拒绝越界值与非法键，
	// 避免调用方以为设置生效、实际被静默丢弃。
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return DynamicMonitorConfig{}, err
	}
	body, err := json.Marshal(config)
	if err != nil {
		return DynamicMonitorConfig{}, fmt.Errorf("编码动态监测规则失败: %w", err)
	}
	if err := s.stations.SetDynamicMonitorConfig(stationID, string(body)); err != nil {
		return DynamicMonitorConfig{}, err
	}
	return config, nil
}

// ApplyDynamicMonitor 在快照同步末尾执行关闭判断与“最少保留账号”补开。
func (s *Service) ApplyDynamicMonitor(ctx context.Context, station *storage.RelayStation) error {
	s.dynamicMonitorMu.Lock()
	defer s.dynamicMonitorMu.Unlock()
	return s.applyDynamicMonitor(ctx, station)
}

func (s *Service) applyDynamicMonitor(ctx context.Context, station *storage.RelayStation) error {
	// 快照采集期间定时任务可能已经重开账号，不能覆盖它刚保存的采样下界。
	station, err := s.stations.FindByID(station.ID)
	if err != nil {
		return err
	}
	config := ParseDynamicMonitorConfig(station.DynamicMonitorConfig)
	if !config.Enabled {
		return nil
	}
	state := ParseDynamicMonitorState(station.DynamicMonitorState)
	accounts, err := s.stations.ListAccounts(station.ID)
	if err != nil {
		return fmt.Errorf("读取账号快照失败: %w", err)
	}
	groups, err := s.stations.ListGroups(station.ID)
	if err != nil {
		return fmt.Errorf("读取分组快照失败: %w", err)
	}
	links, err := s.stations.ListAccountLinks(station.ID)
	if err != nil {
		return fmt.Errorf("读取账号分组关联失败: %w", err)
	}
	plan := planDynamicMonitor(config, state, accounts, groups, links)
	if len(plan.Close) == 0 && len(plan.Reopen) == 0 {
		return s.persistDynamicMonitorStateIfChanged(station.ID, state, plan.State)
	}

	accountByExternalID := make(map[int64]storage.RelayAccount, len(accounts))
	for _, account := range accounts {
		accountByExternalID[account.ExternalID] = account
	}
	accountGroups := make(map[uint][]storage.RelayGroup, len(accounts))
	groupByLocalID := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		groupByLocalID[group.ID] = group
	}
	for _, link := range links {
		if group, ok := groupByLocalID[link.RelayGroupID]; ok {
			accountGroups[link.RelayAccountID] = append(accountGroups[link.RelayAccountID], group)
		}
	}

	nextState := plan.State
	var failures []string
	now := time.Now().UTC()
	for _, externalID := range plan.Close {
		account, ok := accountByExternalID[externalID]
		if !ok {
			continue
		}
		if err := s.setScheduling(ctx, station, account, accountGroups[account.ID], false, sourceDynamicMonitor); err != nil {
			failures = append(failures, fmt.Sprintf("账号 %s 关闭调度: %v", account.Name, err))
			continue
		}
		reason := plan.CloseReasons[externalID]
		if reason == "" {
			reason = dynamicMonitorReasonFirstToken
		}
		nextState.ClosedAccounts = append(nextState.ClosedAccounts, DynamicMonitorClosure{
			AccountExternalID: account.ExternalID,
			AccountName:       account.Name,
			Reason:            reason,
			ClosedAt:          now,
		})
		nextState.consumeSamples(account, now)
	}
	for _, externalID := range plan.Reopen {
		account, ok := accountByExternalID[externalID]
		if !ok {
			continue
		}
		if err := s.setScheduling(ctx, station, account, accountGroups[account.ID], true, sourceDynamicMonitor); err != nil {
			failures = append(failures, fmt.Sprintf("账号 %s 开启调度: %v", account.Name, err))
			continue
		}
		nextState.ClosedAccounts = removeDynamicMonitorClosure(nextState.ClosedAccounts, externalID)
		nextState.consumeSamples(account, time.Now().UTC())
	}
	if err := s.persistDynamicMonitorStateIfChanged(station.ID, state, nextState); err != nil {
		failures = append(failures, fmt.Sprintf("保存动态监测状态: %v", err))
	}
	if len(failures) > 0 {
		return fmt.Errorf("账号动态监测部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

// ReopenTimedDynamicAccounts 由每分钟的独立任务调用：只处理超时自动开启。
func (s *Service) ReopenTimedDynamicAccounts(ctx context.Context) error {
	s.dynamicMonitorMu.Lock()
	defer s.dynamicMonitorMu.Unlock()
	stations, err := s.stations.List()
	if err != nil {
		return err
	}
	var failures []string
	for index := range stations {
		station := &stations[index]
		config := ParseDynamicMonitorConfig(station.DynamicMonitorConfig)
		if !config.Enabled || !config.AllowAutoOpen || config.AutoOpenMinutes <= 0 {
			continue
		}
		state := ParseDynamicMonitorState(station.DynamicMonitorState)
		if len(state.ClosedAccounts) == 0 {
			continue
		}
		accounts, err := s.stations.ListAccounts(station.ID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s 读取账号快照: %v", station.Name, err))
			continue
		}
		links, err := s.stations.ListAccountLinks(station.ID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s 读取账号分组关联: %v", station.Name, err))
			continue
		}
		accountByExternalID := make(map[int64]storage.RelayAccount, len(accounts))
		for _, account := range accounts {
			accountByExternalID[account.ExternalID] = account
		}
		groupByLocalID := make(map[uint]storage.RelayGroup)
		if groups, err := s.stations.ListGroups(station.ID); err == nil {
			for _, group := range groups {
				groupByLocalID[group.ID] = group
			}
		}
		accountGroups := make(map[uint][]storage.RelayGroup, len(links))
		for _, link := range links {
			if group, ok := groupByLocalID[link.RelayGroupID]; ok {
				accountGroups[link.RelayAccountID] = append(accountGroups[link.RelayAccountID], group)
			}
		}

		deadline := time.Now().UTC().Add(-time.Duration(config.AutoOpenMinutes) * time.Minute)
		nextState := retainedDynamicMonitorState(state, accounts)
		for _, closure := range state.ClosedAccounts {
			account, ok := accountByExternalID[closure.AccountExternalID]
			// 账号已删除或已被手动开启：这条记录可以清掉。
			if !ok {
				continue
			}
			nextState.advanceJudged(account.ExternalID, closure.ClosedAt)
			if account.Schedulable {
				nextState.consumeSamples(account, time.Now().UTC())
				continue
			}
			if closure.ClosedAt.After(deadline) {
				nextState.ClosedAccounts = append(nextState.ClosedAccounts, closure)
				continue
			}
			if err := s.setScheduling(ctx, station, account, accountGroups[account.ID], true, sourceDynamicMonitor); err != nil {
				nextState.ClosedAccounts = append(nextState.ClosedAccounts, closure)
				failures = append(failures, fmt.Sprintf("%s 账号 %s 自动开启: %v", station.Name, account.Name, err))
				continue
			}
			nextState.consumeSamples(account, time.Now().UTC())
		}
		if err := s.persistDynamicMonitorStateIfChanged(station.ID, state, nextState); err != nil {
			failures = append(failures, fmt.Sprintf("%s 保存动态监测状态: %v", station.Name, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("账号动态监测自动开启部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

func removeDynamicMonitorClosure(closures []DynamicMonitorClosure, externalID int64) []DynamicMonitorClosure {
	kept := make([]DynamicMonitorClosure, 0, len(closures))
	for _, closure := range closures {
		if closure.AccountExternalID == externalID {
			continue
		}
		kept = append(kept, closure)
	}
	return kept
}

// 手动开启同样建立新一轮采样边界；调用方在远端开启成功后持锁调用。
func (s *Service) resetDynamicMonitorAfterManualOpen(stationID uint, account storage.RelayAccount) error {
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	previous := ParseDynamicMonitorState(station.DynamicMonitorState)
	if !ParseDynamicMonitorConfig(station.DynamicMonitorConfig).Enabled && len(previous.ClosedAccounts) == 0 && len(previous.Judged) == 0 {
		return nil
	}
	next := ParseDynamicMonitorState(station.DynamicMonitorState)
	next.ClosedAccounts = removeDynamicMonitorClosure(next.ClosedAccounts, account.ExternalID)
	next.consumeSamples(account, time.Now().UTC())
	return s.persistDynamicMonitorStateIfChanged(stationID, previous, next)
}

func (s *Service) persistDynamicMonitorStateIfChanged(stationID uint, previous, next DynamicMonitorState) error {
	if len(next.ClosedAccounts) == 0 {
		next.ClosedAccounts = []DynamicMonitorClosure{}
	}
	sort.Slice(next.ClosedAccounts, func(i, j int) bool {
		if !next.ClosedAccounts[i].ClosedAt.Equal(next.ClosedAccounts[j].ClosedAt) {
			return next.ClosedAccounts[i].ClosedAt.Before(next.ClosedAccounts[j].ClosedAt)
		}
		return next.ClosedAccounts[i].AccountExternalID < next.ClosedAccounts[j].AccountExternalID
	})
	nextBody, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("编码动态监测状态失败: %w", err)
	}
	previousBody, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	if string(nextBody) == string(previousBody) {
		return nil
	}
	return s.stations.SetDynamicMonitorState(stationID, string(nextBody))
}
