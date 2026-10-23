package relay

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
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

// dynamicMonitorRuleCategoryMonitor / dynamicMonitorRuleCategoryError 标记关闭记录的规则类别。
const (
	dynamicMonitorRuleCategoryMonitor = "monitor"
	dynamicMonitorRuleCategoryError   = "error"
)

// dynamicMonitorReasonError 是关闭原因：命中全局报错规则组。
const dynamicMonitorReasonError = "error_rule"

const (
	// 阈值按秒配置（比毫秒更贴近实际调参直觉），内部仍换算成毫秒与样本比对。
	dynamicMonitorMinThresholdSeconds     = 0.1
	dynamicMonitorMaxThresholdSeconds     = 600 // 10 分钟
	dynamicMonitorMaxConsecutive          = 100
	dynamicMonitorMaxMinAccounts          = 1000
	dynamicMonitorMaxAutoOpen             = 10080 // 7 天
	dynamicMonitorMaxAccounts             = 1000
	dynamicMonitorMaxGroups               = 20
	dynamicMonitorMaxErrorGroups          = 20
	dynamicMonitorMaxErrorThreshold       = 1000
	dynamicMonitorMaxErrorLookbackMinutes = 10080 // 7 天
	dynamicMonitorErrorPageSize           = 500
	dynamicMonitorMaxErrorPages           = 20

	dynamicMonitorDefaultErrorThreshold = 3
	dynamicMonitorDefaultErrorLookback  = 10

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

// DynamicMonitorErrorRuleGroup 是全局报错规则组：不选择账号，按具体 HTTP
// 状态码统计所有账号的上游最终失败请求，达到阈值即关闭账号。
type DynamicMonitorErrorRuleGroup struct {
	// ID 保持关闭记录与规则的关联，不受改名、重排和同名组影响。
	ID string `json:"id,omitempty"`
	// Name 规则组名称，仅用于界面展示。
	Name string `json:"name"`
	// Enabled 缺省时保持旧配置的启用行为；显式 false 暂停该组。
	Enabled *bool `json:"enabled,omitempty"`
	// StatusCodes 具体 HTTP 状态码，例如 401、429、502；不使用 4xx/5xx 范围。
	StatusCodes []int `json:"status_codes"`
	// Threshold 统计窗口内达到该错误次数后关闭账号。
	Threshold int `json:"threshold"`
	// LookbackMinutes 统计时间窗口（分钟）。
	LookbackMinutes int `json:"lookback_minutes"`
	// AllowAutoOpen 该组关闭的账号是否允许超时自动开启。
	AllowAutoOpen bool `json:"allow_auto_open"`
	// AutoOpenMinutes 关闭多久后自动开启，0 表示不启用。
	AutoOpenMinutes int `json:"auto_open_minutes"`
}

// DynamicMonitorConfig 是弹窗里保存的完整规则。所有字段只属于该中转站的这个功能。
// 阈值与超标次数按规则组各自设置；补开相关的三项是整个中转站通用设置。
type DynamicMonitorConfig struct {
	Enabled bool `json:"enabled"`
	// Groups 多组规则，每组独立的阈值与超标次数。
	Groups []DynamicMonitorRuleGroup `json:"groups"`
	// ErrorGroups 全局报错规则组：不选择账号，按具体状态码统计所有账号。
	ErrorGroups []DynamicMonitorErrorRuleGroup `json:"error_groups,omitempty"`
	// ProtectedAccountExternalIDs 不受本功能任何关闭规则影响，不改变手动或其他功能的开关行为。
	ProtectedAccountExternalIDs []int64 `json:"protected_account_external_ids,omitempty"`
	// MinAccountsPerGroup 通用规则：分组至少保留多少个可用账号，0 表示不设置。
	// 未在 GroupMinAccounts 中单独设置的分组都使用这个公共值。
	MinAccountsPerGroup int `json:"min_accounts_per_group"`
	// GroupMinAccounts 按销售分组（external id 的十进制字符串）单独设置的最少保留账号数。
	// 出现即覆盖 MinAccountsPerGroup；未出现的分组沿用公共值。值为 0 表示该分组不设下限。
	GroupMinAccounts map[string]int `json:"group_min_accounts,omitempty"`
	// GroupRuleGroups 销售分组（external id 的十进制字符串）关联的规则组下标（0 基，指向 Groups）。
	// 同步时该分组下“首次出现”的新账号会自动加入对应规则组；未出现的分组不自动关联。
	GroupRuleGroups map[string]int `json:"group_rule_groups,omitempty"`
	// GroupRuleExcluded 销售分组（external id 的十进制字符串）是否不参与规则判断。
	// 值为 true 表示该分组不参与“分组最少保留账号数”的补开判断：既不占用下限，
	// 也不会因为下限不足把组内刚被关闭的账号立刻开回来。未出现或 false 表示参与，
	// 兼容没有该字段的旧规则（旧规则一律视为参与）。
	GroupRuleExcluded map[string]bool `json:"group_rule_excluded,omitempty"`
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

// GroupRuleGroup 返回销售分组关联的规则组下标；未关联或下标越界时返回 -1。
func (c DynamicMonitorConfig) GroupRuleGroup(groupExternalID int64) int {
	index, ok := c.GroupRuleGroups[strconv.FormatInt(groupExternalID, 10)]
	if !ok || index < 0 || index >= len(c.Groups) {
		return -1
	}
	return index
}

// GroupParticipates 返回销售分组是否参与规则判断（默认为参与）。
// 不参与的分组既不占用“分组最少保留账号数”，也不会因为下限不足被补开。
func (c DynamicMonitorConfig) GroupParticipates(groupExternalID int64) bool {
	return !c.GroupRuleExcluded[strconv.FormatInt(groupExternalID, 10)]
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
	AccountExternalID int64  `json:"account_external_id"`
	AccountName       string `json:"account_name"`
	Reason            string `json:"reason"`
	// RuleCategory 标记关闭来源：monitor 为监测规则组，error 为报错规则组。
	RuleCategory string `json:"rule_category,omitempty"`
	// RuleGroupName 是命中规则组的名称，便于界面直接展示。
	RuleGroupName string `json:"rule_group_name,omitempty"`
	RuleGroupID   string `json:"rule_group_id,omitempty"`
	// ErrorStatusCodes/ErrorCount/ErrorWindowMinutes 说明报错组命中的状态码、次数与统计窗口。
	ErrorStatusCodes   []int     `json:"error_status_codes,omitempty"`
	ErrorCount         int64     `json:"error_count,omitempty"`
	ErrorWindowMinutes int       `json:"error_window_minutes,omitempty"`
	ClosedAt           time.Time `json:"closed_at"`
	// AutoOpenAt 是本条记录允许自动开启的时间，nil 表示只能靠分组保底开启。
	AutoOpenAt *time.Time `json:"auto_open_at,omitempty"`
}

// DynamicMonitorState 是运行状态：只有这里记录的账号才会被自动开启。
type DynamicMonitorState struct {
	ClosedAccounts []DynamicMonitorClosure `json:"closed_accounts"`
	// Judged 是关闭判定的采样下界（不含）：关闭成功时消费本轮样本，
	// 重开成功时推进到开启时间。只有下界之后的新调用参与连续超标计数，
	// 不把重开前的旧样本拼进新一轮。普通快照不会推进下界，以便跨同步累计。
	// 持久化保留到账号删除，兼容没有该字段的旧运行状态。
	Judged map[int64]time.Time `json:"judged,omitempty"`
	// SeenAccounts 记录所有快照里出现过的账号（external id）。
	// 只有“首次出现”的账号才会被自动加入关联规则组，之后即使被手动移除也不再自动加回。
	// 持久化保留到账号删除，兼容没有该字段的旧运行状态（旧状态视为全部已见过）。
	SeenAccounts map[int64]struct{} `json:"seen_accounts,omitempty"`
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
// 只保留当前仍存在的账号的采样下界与“已见过”记录，账号删除后自动清理。
func retainedDynamicMonitorState(state DynamicMonitorState, accounts []storage.RelayAccount) DynamicMonitorState {
	next := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{}}
	for _, account := range accounts {
		next.advanceJudged(account.ExternalID, state.Judged[account.ExternalID])
		if _, seen := state.SeenAccounts[account.ExternalID]; seen {
			next.markSeen(account.ExternalID)
		}
	}
	return next
}

// markSeen 记录账号已经出现过，用于判断下次同步是否属于“新增”。
func (state *DynamicMonitorState) markSeen(externalID int64) {
	if state.SeenAccounts == nil {
		state.SeenAccounts = make(map[int64]struct{})
	}
	state.SeenAccounts[externalID] = struct{}{}
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
	c.ProtectedAccountExternalIDs = normalizeDynamicMonitorAccountIDs(c.ProtectedAccountExternalIDs)
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
	// 只保留“明确不参与”的分组；false 视为参与，等同于没设置，避免落库冗余。
	if len(c.GroupRuleExcluded) > 0 {
		excluded := make(map[string]bool, len(c.GroupRuleExcluded))
		for externalID, value := range c.GroupRuleExcluded {
			if value {
				excluded[externalID] = true
			}
		}
		if len(excluded) == 0 {
			excluded = nil
		}
		c.GroupRuleExcluded = excluded
	}
	// 报错规则组：没有有效状态码的空壳组直接丢弃，避免界面出现无法命中的规则。
	errorGroups := make([]DynamicMonitorErrorRuleGroup, 0, len(c.ErrorGroups))
	for index, group := range c.ErrorGroups {
		normalized := group.Normalize()
		if len(normalized.StatusCodes) == 0 {
			continue
		}
		if normalized.ID == "" {
			// 旧配置在首次保存前也需有稳定标识；开关不参与标识，禁用不会改换身份。
			identity := normalized
			identity.Enabled = nil
			body, _ := json.Marshal(identity)
			digest := sha256.Sum256(body)
			normalized.ID = fmt.Sprintf("error-%x-%d", digest[:12], index)
		}
		errorGroups = append(errorGroups, normalized)
	}
	c.ErrorGroups = errorGroups
	claimed := make(map[int64]struct{})
	groups := make([]DynamicMonitorRuleGroup, 0, len(c.Groups))
	// 规则组下标会被“丢弃空组”改变，这里记录旧下标 → 新下标，用于重映射分组关联。
	remap := make(map[int]int, len(c.Groups))
	for oldIndex, group := range c.Groups {
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
		remap[oldIndex] = len(groups)
		groups = append(groups, normalized)
	}
	c.Groups = groups
	c.GroupRuleGroups = remapGroupRuleGroups(c.GroupRuleGroups, remap)
	return c
}

// remapGroupRuleGroups 按规则组重排后的下标修正分组关联；指向已删除规则组的关联被丢弃。
func remapGroupRuleGroups(source map[string]int, remap map[int]int) map[string]int {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]int, len(source))
	for externalID, index := range source {
		next, ok := remap[index]
		if !ok {
			continue
		}
		result[externalID] = next
	}
	if len(result) == 0 {
		return nil
	}
	return result
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
	g.AccountExternalIDs = normalizeDynamicMonitorAccountIDs(g.AccountExternalIDs)
	return g
}

func normalizeDynamicMonitorAccountIDs(source []int64) []int64 {
	ids := make([]int64, 0, len(source))
	seen := make(map[int64]struct{}, len(source))
	for _, id := range source {
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
	return ids
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

// Normalize 补齐报错组的默认值，并按升序去重状态码。
func (g DynamicMonitorErrorRuleGroup) Normalize() DynamicMonitorErrorRuleGroup {
	g.Name = strings.TrimSpace(g.Name)
	g.ID = strings.TrimSpace(g.ID)
	if g.Enabled == nil {
		enabled := true
		g.Enabled = &enabled
	}
	if g.Threshold <= 0 {
		g.Threshold = dynamicMonitorDefaultErrorThreshold
	}
	if g.LookbackMinutes <= 0 {
		g.LookbackMinutes = dynamicMonitorDefaultErrorLookback
	}
	if g.AutoOpenMinutes < 0 {
		g.AutoOpenMinutes = 0
	}
	seen := make(map[int]struct{}, len(g.StatusCodes))
	codes := make([]int, 0, len(g.StatusCodes))
	for _, code := range g.StatusCodes {
		if code < 400 || code > 599 {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Ints(codes)
	g.StatusCodes = codes
	return g
}

// IsEnabled 兼容没有 enabled 字段的旧配置。
func (g DynamicMonitorErrorRuleGroup) IsEnabled() bool {
	return g.Enabled == nil || *g.Enabled
}

// errorRuleIndex 优先按稳定 ID 关联；旧记录沿用命中靠前报错组的优先级。
func (c DynamicMonitorConfig) errorRuleIndex(id, name string, codes []int, window int, count int64) int {
	for index, group := range c.ErrorGroups {
		if id != "" {
			if group.ID == id {
				return index
			}
			continue
		}
		if count > 0 && count < int64(group.Threshold) {
			continue
		}
		if group.Label(index) == name && (len(codes) == 0 || slices.Equal(group.StatusCodes, codes)) && (window <= 0 || group.LookbackMinutes == window) {
			return index
		}
	}
	return -1
}

func (c DynamicMonitorConfig) errorRuleEnabled(id, name string, codes []int, window int, count int64) bool {
	index := c.errorRuleIndex(id, name, codes, window, count)
	return index < 0 || c.ErrorGroups[index].IsEnabled()
}

// 保存新配置前按旧配置绑定历史关闭记录，避免改名或重排让停用开关失效。
func bindDynamicMonitorErrorRuleIDs(state DynamicMonitorState, config DynamicMonitorConfig) (DynamicMonitorState, bool) {
	state.ClosedAccounts = slices.Clone(state.ClosedAccounts)
	changed := false
	for index, closure := range state.ClosedAccounts {
		if closure.RuleGroupID != "" || (closure.RuleCategory != dynamicMonitorRuleCategoryError && closure.Reason != dynamicMonitorReasonError) {
			continue
		}
		groupIndex := config.errorRuleIndex("", closure.RuleGroupName, closure.ErrorStatusCodes, closure.ErrorWindowMinutes, closure.ErrorCount)
		if groupIndex >= 0 && config.ErrorGroups[groupIndex].ID != "" {
			state.ClosedAccounts[index].RuleGroupID = config.ErrorGroups[groupIndex].ID
			changed = true
		}
	}
	return state, changed
}

// Label 返回报错组在提示与关闭记录里用的称呼。
func (g DynamicMonitorErrorRuleGroup) Label(index int) string {
	if g.Name != "" {
		return g.Name
	}
	return fmt.Sprintf("报错组 %d", index+1)
}

// Validate 校验弹窗提交的规则。
func (c DynamicMonitorConfig) Validate() error {
	if len(c.ProtectedAccountExternalIDs) > dynamicMonitorMaxAccounts {
		return fmt.Errorf("保护账号不能超过 %d 个", dynamicMonitorMaxAccounts)
	}
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
	for externalID, index := range c.GroupRuleGroups {
		id, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("分组关联规则组的分组 ID %q 不合法", externalID)
		}
		if index < 0 || index >= len(c.Groups) {
			return fmt.Errorf("分组 %d 关联的规则组下标 %d 超出范围", id, index)
		}
	}
	for externalID := range c.GroupRuleExcluded {
		id, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("分组是否参与规则判断的分组 ID %q 不合法", externalID)
		}
	}
	if len(c.ErrorGroups) > dynamicMonitorMaxErrorGroups {
		return fmt.Errorf("报错规则组不能超过 %d 个", dynamicMonitorMaxErrorGroups)
	}
	errorGroupIDs := make(map[string]struct{}, len(c.ErrorGroups))
	for index, group := range c.ErrorGroups {
		label := group.Label(index)
		if len(group.ID) > 128 {
			return fmt.Errorf("%s 规则标识不能超过 128 个字符", label)
		}
		if group.ID != "" {
			if _, exists := errorGroupIDs[group.ID]; exists {
				return fmt.Errorf("%s 规则标识重复", label)
			}
			errorGroupIDs[group.ID] = struct{}{}
		}
		if len(group.StatusCodes) == 0 {
			return fmt.Errorf("%s 请至少填写一个 HTTP 状态码", label)
		}
		for _, code := range group.StatusCodes {
			if code < 400 || code > 599 {
				return fmt.Errorf("%s 状态码 %d 不合法，请填写 400 到 599 之间的具体状态码", label, code)
			}
		}
		if group.Threshold < 1 || group.Threshold > dynamicMonitorMaxErrorThreshold {
			return fmt.Errorf("%s 错误次数阈值必须在 1 到 %d 之间", label, dynamicMonitorMaxErrorThreshold)
		}
		if group.LookbackMinutes < 1 || group.LookbackMinutes > dynamicMonitorMaxErrorLookbackMinutes {
			return fmt.Errorf("%s 统计时间窗口必须在 1 到 %d 分钟之间", label, dynamicMonitorMaxErrorLookbackMinutes)
		}
		if group.AutoOpenMinutes < 0 || group.AutoOpenMinutes > dynamicMonitorMaxAutoOpen {
			return fmt.Errorf("%s 自动开启时间必须在 0 到 %d 分钟之间", label, dynamicMonitorMaxAutoOpen)
		}
	}
	if c.AutoOpenMinutes < 0 || c.AutoOpenMinutes > dynamicMonitorMaxAutoOpen {
		return fmt.Errorf("自动开启时间必须在 0 到 %d 分钟之间", dynamicMonitorMaxAutoOpen)
	}
	if c.Enabled && len(c.Groups) == 0 && len(c.ErrorGroups) == 0 {
		return errors.New("启用监测时请至少配置一个规则组或报错规则组")
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

// autoAssignNewAccounts 把“分组规则”关联的新账号加入对应规则组。
//
// 规则：
//   - 只有本次快照里“首次出现”的账号才自动加入；已见过（含曾被手动移除）的账号不再自动加回。
//   - 账号按其所属销售分组找关联规则组；同时属于多个已关联分组时，取分组倍率（rate_multiplier）最高的那个。
//   - 已属于任一规则组的账号不重复处理；一个账号只归属一个规则组。
//   - SeenAccounts 为空（旧状态或首次启用）时只建立基线，不自动加入，避免把存量账号一次性塞进规则组。
//   - 本轮快照里还没有任何销售分组的账号不记为“已见过”，留到下次快照再判定。
//
// 传入的 state 是 retainedDynamicMonitorState 的结果，函数就地更新它的 SeenAccounts。
func autoAssignNewAccounts(
	config DynamicMonitorConfig,
	previous DynamicMonitorState,
	accounts []storage.RelayAccount,
	groups []storage.RelayGroup,
	links []storage.RelayAccountGroup,
	state *DynamicMonitorState,
) (DynamicMonitorConfig, bool) {
	// 结构体按值传递仍会共享 Groups 的底层数组，必须先复制外层切片。
	// 否则写入新账号会同时改写调用方的旧配置，导致持久化比较误判为没有变化。
	// 组内账号名单在合并时另建切片，其余配置只读。
	config.Groups = slices.Clone(config.Groups)
	// 没有配置任何分组关联时，只需维护“已见过”基线。
	hasRuleGroups := len(config.GroupRuleGroups) > 0
	coldStart := len(previous.SeenAccounts) == 0

	// 账号当前是否已属于某个规则组，避免重复加入。
	memberOfRuleGroup := make(map[int64]struct{})
	for _, group := range config.Groups {
		for _, externalID := range group.AccountExternalIDs {
			memberOfRuleGroup[externalID] = struct{}{}
		}
	}

	groupByLocalID := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		groupByLocalID[group.ID] = group
	}
	// 冷启动按所有已同步分组建立存量基线；日常同步只追踪已映射规则组的账号。
	linkedGroupsByAccount := make(map[uint][]storage.RelayGroup)
	knownGroupLink := make(map[uint]struct{}, len(links))
	ruleGroupLink := make(map[uint]struct{}, len(links))
	for _, link := range links {
		group, ok := groupByLocalID[link.RelayGroupID]
		if !ok {
			continue
		}
		knownGroupLink[link.RelayAccountID] = struct{}{}
		if config.GroupRuleGroup(group.ExternalID) < 0 {
			continue
		}
		ruleGroupLink[link.RelayAccountID] = struct{}{}
		linkedGroupsByAccount[link.RelayAccountID] = append(linkedGroupsByAccount[link.RelayAccountID], group)
	}

	seenThisPass := make(map[int64]struct{}, len(accounts))
	for externalID := range memberOfRuleGroup {
		seenThisPass[externalID] = struct{}{}
	}
	for _, account := range accounts {
		if coldStart {
			if _, linked := knownGroupLink[account.ID]; linked {
				seenThisPass[account.ExternalID] = struct{}{}
			}
		} else if _, linked := ruleGroupLink[account.ID]; linked {
			seenThisPass[account.ExternalID] = struct{}{}
		}
	}

	if hasRuleGroups && !coldStart {

		// 收集本轮要加入各规则组的账号；按外部 ID 升序保证结果稳定。
		pending := make(map[int][]int64)
		for _, account := range accounts {
			if _, seen := previous.SeenAccounts[account.ExternalID]; seen {
				continue
			}
			if _, already := memberOfRuleGroup[account.ExternalID]; already {
				continue
			}
			bestIndex := -1
			bestRate := 0.0
			for _, group := range linkedGroupsByAccount[account.ID] {
				index := config.GroupRuleGroup(group.ExternalID)
				if index < 0 {
					continue
				}
				// 倍率更高者胜出；倍率相等时取更靠前的规则组，保证结果稳定可预期。
				if bestIndex < 0 || group.RateMultiplier > bestRate || (group.RateMultiplier == bestRate && index < bestIndex) {
					bestIndex = index
					bestRate = group.RateMultiplier
				}
			}
			if bestIndex < 0 {
				continue
			}
			pending[bestIndex] = append(pending[bestIndex], account.ExternalID)
		}

		assigned := false
		for index, ids := range pending {
			if len(ids) == 0 {
				continue
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			merged := append([]int64{}, config.Groups[index].AccountExternalIDs...)
			for _, id := range ids {
				merged = append(merged, id)
				memberOfRuleGroup[id] = struct{}{}
			}
			config.Groups[index].AccountExternalIDs = merged
			assigned = true
		}
		// 只提交本轮判定完成的账号，未映射账号保留待处理状态。
		markSeenAccounts(seenThisPass, state)
		return config, assigned
	}

	// 未配置分组关联或处于冷启动：建立安全基线，不批量加入存量账号。
	markSeenAccounts(seenThisPass, state)
	return config, false
}

func markSeenAccounts(accountIDs map[int64]struct{}, state *DynamicMonitorState) {
	for externalID := range accountIDs {
		state.markSeen(externalID)
	}
}

// planRuleGroupAssignments 计算“明确新增”的账号应加入的规则组。
//
// 规则与 autoAssignNewAccounts 保持一致：账号按所属销售分组找关联规则组，同时命中多个
// 已关联分组时取分组倍率最高者（倍率相同取下标更小的规则组）；已属于任一规则组的账号跳过。
// 返回 规则组下标 → 待追加的账号 external id（升序）。
func planRuleGroupAssignments(
	config DynamicMonitorConfig,
	accountExternalIDs []int64,
	accounts []storage.RelayAccount,
	groups []storage.RelayGroup,
	links []storage.RelayAccountGroup,
) map[int][]int64 {
	if len(accountExternalIDs) == 0 || len(config.GroupRuleGroups) == 0 || len(config.Groups) == 0 {
		return nil
	}
	member := make(map[int64]struct{})
	for _, group := range config.Groups {
		for _, externalID := range group.AccountExternalIDs {
			member[externalID] = struct{}{}
		}
	}
	groupByLocalID := make(map[uint]storage.RelayGroup, len(groups))
	for _, group := range groups {
		groupByLocalID[group.ID] = group
	}
	linkedGroupsByAccount := make(map[uint][]storage.RelayGroup, len(links))
	for _, link := range links {
		group, ok := groupByLocalID[link.RelayGroupID]
		if !ok {
			continue
		}
		linkedGroupsByAccount[link.RelayAccountID] = append(linkedGroupsByAccount[link.RelayAccountID], group)
	}
	accountByExternalID := make(map[int64]storage.RelayAccount, len(accounts))
	for _, account := range accounts {
		accountByExternalID[account.ExternalID] = account
	}

	result := make(map[int][]int64)
	for _, externalID := range accountExternalIDs {
		if _, already := member[externalID]; already {
			continue
		}
		account, ok := accountByExternalID[externalID]
		if !ok {
			continue
		}
		bestIndex := -1
		bestRate := 0.0
		for _, group := range linkedGroupsByAccount[account.ID] {
			index := config.GroupRuleGroup(group.ExternalID)
			if index < 0 {
				continue
			}
			if bestIndex < 0 || group.RateMultiplier > bestRate || (group.RateMultiplier == bestRate && index < bestIndex) {
				bestIndex = index
				bestRate = group.RateMultiplier
			}
		}
		if bestIndex < 0 {
			continue
		}
		// 同一批里重复传入同一个账号时只入组一次。
		member[externalID] = struct{}{}
		result[bestIndex] = append(result[bestIndex], externalID)
	}
	for index := range result {
		ids := result[index]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	return result
}

// AssignNewAccountsToRuleGroups 把“明确新增”的账号加入其分组关联的规则组。
//
// 快照同步里的 autoAssignNewAccounts 靠 SeenAccounts 判断“首次出现”，而同步本身会把本轮
// 所有账号记为已见过：站点处于冷启动（SeenAccounts 为空）时同步只建立基线，本批新增账号
// 就被记成已见过却从未入组，之后也不会再有机会。复制新增（BatchCloneAccounts）明确知道
// 哪些账号是新的，因此在同步之后单独走这条路径，不依赖 SeenAccounts。
func (s *Service) AssignNewAccountsToRuleGroups(stationID uint, accountExternalIDs []int64) error {
	if len(accountExternalIDs) == 0 {
		return nil
	}
	s.dynamicMonitorMu.Lock()
	defer s.dynamicMonitorMu.Unlock()
	station, err := s.stations.FindByID(stationID)
	if err != nil {
		return err
	}
	config := ParseDynamicMonitorConfig(station.DynamicMonitorConfig)
	// 未启用动态监测或没有分组关联时无需处理，保持“不启用就不入组”的既有语义。
	if !config.Enabled || len(config.GroupRuleGroups) == 0 {
		return nil
	}
	accounts, err := s.stations.ListAccounts(stationID)
	if err != nil {
		return fmt.Errorf("读取账号快照失败: %w", err)
	}
	groups, err := s.stations.ListGroups(stationID)
	if err != nil {
		return fmt.Errorf("读取分组快照失败: %w", err)
	}
	links, err := s.stations.ListAccountLinks(stationID)
	if err != nil {
		return fmt.Errorf("读取账号分组关联失败: %w", err)
	}
	assignments := planRuleGroupAssignments(config, accountExternalIDs, accounts, groups, links)
	if len(assignments) == 0 {
		return nil
	}
	for index, ids := range assignments {
		if index < 0 || index >= len(config.Groups) {
			continue
		}
		merged := append([]int64{}, config.Groups[index].AccountExternalIDs...)
		config.Groups[index].AccountExternalIDs = append(merged, ids...)
	}
	body, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("编码动态监测规则失败: %w", err)
	}
	return s.stations.SetDynamicMonitorConfig(stationID, string(body))
}

// dynamicMonitorPlan 是一次纯计算结果，便于单独测试。
// dynamicMonitorErrorHit 是一个账号命中的报错规则组信息，用于写入关闭记录。
type dynamicMonitorErrorHit struct {
	RuleGroupName   string
	RuleGroupID     string
	StatusCodes     []int
	Count           int64
	WindowMinutes   int
	AllowAutoOpen   bool
	AutoOpenMinutes int
}

// dynamicMonitorPlan 是一次纯计算结果，便于单独测试。
type dynamicMonitorPlan struct {
	// Close 本轮应关闭的账号（external id）。
	Close []int64
	// CloseReasons 记录每个待关闭账号命中的原因，写入关闭记录用于界面区分。
	CloseReasons map[int64]string
	// CloseRuleGroups 记录普通规则组关闭时命中的规则组名称。
	CloseRuleGroups map[int64]string
	// CloseErrorHits 记录报错规则组关闭时命中的错误信息。
	CloseErrorHits map[int64]dynamicMonitorErrorHit
	// Reopen 本轮应开启的账号（external id），只来自之前被规则关闭的账号。
	Reopen []int64
	// State 是清理过期关闭记录后的状态，执行成功后再补上本轮关闭记录。
	State DynamicMonitorState
	// Config 是本轮计算后（可能已被自动加入改写账号名单）的规则，调用方据此持久化。
	Config DynamicMonitorConfig
}

// planDynamicMonitor 计算关闭与补开名单。输入全部来自最近一次快照，不触发任何写操作。
func planDynamicMonitor(
	config DynamicMonitorConfig,
	state DynamicMonitorState,
	accounts []storage.RelayAccount,
	groups []storage.RelayGroup,
	links []storage.RelayAccountGroup,
) dynamicMonitorPlan {
	return planDynamicMonitorWithErrors(config, state, accounts, groups, links, nil)
}

// planDynamicMonitorWithErrors 在普通规则组之外叠加全局报错规则组的命中结果。
func planDynamicMonitorWithErrors(
	config DynamicMonitorConfig,
	state DynamicMonitorState,
	accounts []storage.RelayAccount,
	groups []storage.RelayGroup,
	links []storage.RelayAccountGroup,
	errorHits map[int64]dynamicMonitorErrorHit,
) dynamicMonitorPlan {
	plan := dynamicMonitorPlan{
		State:           retainedDynamicMonitorState(state, accounts),
		CloseReasons:    make(map[int64]string),
		CloseRuleGroups: make(map[int64]string),
		CloseErrorHits:  make(map[int64]dynamicMonitorErrorHit),
	}
	// 0) 分组关联：把关联销售分组下“首次出现”的新账号自动加入对应规则组。
	// 这一步会改写 config.Groups 的账号名单，必须在关闭判定之前执行，加入即参与监测。
	config, _ = autoAssignNewAccounts(config, state, accounts, groups, links, &plan.State)
	plan.Config = config

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

	// 2) 关闭账号：普通规则组按首字延迟判定，报错规则组对所有账号生效。
	ruleByAccount := make(map[int64]DynamicMonitorRuleGroup)
	ruleNameByAccount := make(map[int64]string)
	for index, group := range config.Groups {
		name := group.Label(index)
		for _, externalID := range group.AccountExternalIDs {
			ruleByAccount[externalID] = group
			ruleNameByAccount[externalID] = name
		}
	}
	markClose := func(externalID int64, reason string) bool {
		if _, done := plan.CloseReasons[externalID]; done {
			return false
		}
		plan.Close = append(plan.Close, externalID)
		plan.CloseReasons[externalID] = reason
		return true
	}

	for _, account := range accounts {
		if !relayAccountSchedulable(account) || slices.Contains(config.ProtectedAccountExternalIDs, account.ExternalID) {
			continue
		}
		if _, alreadyClosed := closed[account.ExternalID]; alreadyClosed {
			continue
		}
		closedNow := false
		if rule, ok := ruleByAccount[account.ExternalID]; ok {
			samples := dynamicMonitorSamplesAfter(account.LatencySamplesJSON, plan.State.Judged[account.ExternalID])
			switch {
			case anyFirstTokenOver(samples, rule.singleTimeoutMS()):
				closedNow = markClose(account.ExternalID, dynamicMonitorReasonSingleFirstToken)
			case consecutiveFirstTokenOver(samples, rule.thresholdMS()) >= rule.ConsecutiveCount:
				closedNow = markClose(account.ExternalID, dynamicMonitorReasonFirstToken)
			}
			if closedNow {
				plan.CloseRuleGroups[account.ExternalID] = ruleNameByAccount[account.ExternalID]
				continue
			}
		}
		if hit, ok := errorHits[account.ExternalID]; ok && hit.Count > 0 && config.errorRuleEnabled(hit.RuleGroupID, hit.RuleGroupName, hit.StatusCodes, hit.WindowMinutes, hit.Count) {
			if markClose(account.ExternalID, dynamicMonitorReasonError) {
				plan.CloseErrorHits[account.ExternalID] = hit
			}
		}
	}
	sort.Slice(plan.Close, func(i, j int) bool { return plan.Close[i] < plan.Close[j] })

	// 3) 分组最少保留账号数：无论自动开启如何设置，最终都要保住分组实际可用账号数。
	if !config.anyGroupMinFloor() {
		return plan
	}
	membersByGroupID := make(map[uint][]storage.RelayAccount, len(groups))
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
	// 分组保底保持现有逻辑：先按规则关闭，再在可用账号低于下限时补开之前被规则关闭的账号。
	plannedReopen := make(map[int64]struct{})
	for _, group := range groups {
		if !isActive(group.Status) || !config.GroupParticipates(group.ExternalID) {
			continue
		}
		floor := config.GroupMinFloor(group.ExternalID)
		if floor <= 0 {
			continue
		}
		available := 0
		for _, account := range membersByGroupID[group.ID] {
			if !relayAccountSchedulable(account) {
				continue
			}
			if _, closing := plannedClose[account.ExternalID]; closing {
				continue
			}
			available++
		}
		if available >= floor {
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
			if available >= floor {
				break
			}
			if _, done := plannedReopen[candidate.AccountExternalID]; done {
				continue
			}
			plannedReopen[candidate.AccountExternalID] = struct{}{}
			plan.Reopen = append(plan.Reopen, candidate.AccountExternalID)
			available++
		}
	}
	sort.Slice(plan.Reopen, func(i, j int) bool { return plan.Reopen[i] < plan.Reopen[j] })
	return plan
}

// SaveDynamicMonitorConfig 保存当前中转站的动态监测规则。
func (s *Service) SaveDynamicMonitorConfig(stationID uint, config DynamicMonitorConfig) (DynamicMonitorConfig, error) {
	// 分组关联的规则组下标先按原始输入校验范围：Normalize 会因丢弃空规则组而下标前移，
	// 若先规范化再校验，越界下标会被静默丢弃，调用方以为设置生效、实际没保存。
	if err := validateGroupRuleGroupRange(config.GroupRuleGroups, len(config.Groups)); err != nil {
		return DynamicMonitorConfig{}, err
	}
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		return DynamicMonitorConfig{}, err
	}
	body, err := json.Marshal(config)
	if err != nil {
		return DynamicMonitorConfig{}, fmt.Errorf("编码动态监测规则失败: %w", err)
	}
	s.dynamicMonitorMu.Lock()
	defer s.dynamicMonitorMu.Unlock()
	previous, err := s.stations.FindByID(stationID)
	if err != nil {
		return DynamicMonitorConfig{}, err
	}
	state, changed := bindDynamicMonitorErrorRuleIDs(ParseDynamicMonitorState(previous.DynamicMonitorState), ParseDynamicMonitorConfig(previous.DynamicMonitorConfig))
	if changed {
		stateBody, err := json.Marshal(state)
		if err != nil {
			return DynamicMonitorConfig{}, fmt.Errorf("编码动态监测状态失败: %w", err)
		}
		if err := s.stations.SetDynamicMonitorState(stationID, string(stateBody)); err != nil {
			return DynamicMonitorConfig{}, err
		}
	}
	if err := s.stations.SetDynamicMonitorConfig(stationID, string(body)); err != nil {
		return DynamicMonitorConfig{}, err
	}
	return config, nil
}

// validateGroupRuleGroupRange 按原始（未规范化）输入校验分组关联下标是否合法。
func validateGroupRuleGroupRange(assignments map[string]int, groupCount int) error {
	for externalID, index := range assignments {
		id, err := strconv.ParseInt(strings.TrimSpace(externalID), 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("分组关联规则组的分组 ID %q 不合法", externalID)
		}
		if index < 0 || index >= groupCount {
			return fmt.Errorf("分组 %d 关联的规则组下标 %d 超出范围", id, index)
		}
	}
	return nil
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
	// 报错规则组按具体状态码统计所有账号，统计不完整时本轮不关闭，避免误判。
	errorHits := s.dynamicMonitorErrorHits(ctx, station, config, state, accounts)
	plan := planDynamicMonitorWithErrors(config, state, accounts, groups, links, errorHits)
	// 自动加入改写了账号名单：先落库，保证下次同步与关闭判定都用最新名单。
	if err := s.persistDynamicMonitorConfigIfChanged(station.ID, config, plan.Config); err != nil {
		return err
	}
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
		closure := DynamicMonitorClosure{
			AccountExternalID: account.ExternalID,
			AccountName:       account.Name,
			Reason:            reason,
			ClosedAt:          now,
		}
		if reason == dynamicMonitorReasonError {
			hit := plan.CloseErrorHits[externalID]
			closure.RuleCategory = dynamicMonitorRuleCategoryError
			closure.RuleGroupName = hit.RuleGroupName
			closure.RuleGroupID = hit.RuleGroupID
			closure.ErrorStatusCodes = hit.StatusCodes
			closure.ErrorCount = hit.Count
			closure.ErrorWindowMinutes = hit.WindowMinutes
			closure.AutoOpenAt = autoOpenDeadline(now, hit.AllowAutoOpen, hit.AutoOpenMinutes)
		} else {
			closure.RuleCategory = dynamicMonitorRuleCategoryMonitor
			closure.RuleGroupName = plan.CloseRuleGroups[externalID]
			closure.AutoOpenAt = autoOpenDeadline(now, config.AllowAutoOpen, config.AutoOpenMinutes)
		}
		nextState.ClosedAccounts = append(nextState.ClosedAccounts, closure)
		nextState.consumeSamples(account, now)
	}
	if err := s.persistDynamicMonitorStateIfChanged(station.ID, state, nextState); err != nil {
		failures = append(failures, fmt.Sprintf("保存动态监测状态: %v", err))
	}
	if len(failures) > 0 {
		return fmt.Errorf("账号动态监测部分失败: %s", strings.Join(failures, "; "))
	}
	return nil
}

// ReopenTimedDynamicAccounts 由每分钟的独立任务调用：按关闭记录里的自动开启时间开启账号。
func (s *Service) ReopenTimedDynamicAccounts(ctx context.Context) error {
	s.dynamicMonitorMu.Lock()
	defer s.dynamicMonitorMu.Unlock()
	stations, err := s.stations.List()
	if err != nil {
		return err
	}
	var failures []string
	now := time.Now().UTC()
	for index := range stations {
		station := &stations[index]
		config := ParseDynamicMonitorConfig(station.DynamicMonitorConfig)
		if !config.Enabled {
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

		nextState := retainedDynamicMonitorState(state, accounts)
		for _, closure := range state.ClosedAccounts {
			account, ok := accountByExternalID[closure.AccountExternalID]
			// 账号已删除或已被手动开启：这条记录可以清掉。
			if !ok {
				continue
			}
			nextState.advanceJudged(account.ExternalID, closure.ClosedAt)
			if account.Schedulable {
				nextState.consumeSamples(account, now)
				continue
			}
			deadline := dynamicMonitorClosureDeadline(closure, config)
			if deadline == nil || now.Before(*deadline) {
				nextState.ClosedAccounts = append(nextState.ClosedAccounts, closure)
				continue
			}
			if err := s.setScheduling(ctx, station, account, accountGroups[account.ID], true, sourceDynamicMonitor); err != nil {
				nextState.ClosedAccounts = append(nextState.ClosedAccounts, closure)
				failures = append(failures, fmt.Sprintf("%s 账号 %s 自动开启: %v", station.Name, account.Name, err))
				continue
			}
			nextState.consumeSamples(account, now)
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

// dynamicMonitorClosureDeadline 返回关闭记录的实际自动开启时间。
// 旧版记录没有 AutoOpenAt，回落到整站通用配置，保证升级后仍能按时开启。
func dynamicMonitorClosureDeadline(closure DynamicMonitorClosure, config DynamicMonitorConfig) *time.Time {
	if (closure.RuleCategory == dynamicMonitorRuleCategoryError || closure.Reason == dynamicMonitorReasonError) && !config.errorRuleEnabled(closure.RuleGroupID, closure.RuleGroupName, closure.ErrorStatusCodes, closure.ErrorWindowMinutes, closure.ErrorCount) {
		return nil
	}
	if closure.AutoOpenAt != nil {
		return closure.AutoOpenAt
	}
	if closure.RuleCategory != "" {
		return nil
	}
	return autoOpenDeadline(closure.ClosedAt, config.AllowAutoOpen, config.AutoOpenMinutes)
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

// persistDynamicMonitorConfigIfChanged 只在规则实际被改写时写库，避免每次同步都产生写入。
func (s *Service) persistDynamicMonitorConfigIfChanged(stationID uint, previous, next DynamicMonitorConfig) error {
	nextBody, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("编码动态监测规则失败: %w", err)
	}
	previousBody, err := json.Marshal(previous)
	if err != nil {
		return err
	}
	if string(nextBody) == string(previousBody) {
		return nil
	}
	return s.stations.SetDynamicMonitorConfig(stationID, string(nextBody))
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

// dynamicMonitorErrorEvent 是一条按状态码筛选出来的上游最终错误。
type dynamicMonitorErrorEvent struct {
	AccountExternalID int64
	CreatedAt         time.Time
	StatusCode        int
}

// dynamicMonitorErrorEndpoint 按具体状态码统计账号错误，status_codes 使用逗号分隔。
func dynamicMonitorErrorEndpoint(baseURL string, statusCodes []int, since, until time.Time, page, pageSize int) string {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	query.Set("page_size", strconv.Itoa(pageSize))
	query.Set("start_time", since.Format(time.RFC3339))
	query.Set("end_time", until.Format(time.RFC3339))
	codes := make([]string, 0, len(statusCodes))
	for _, code := range statusCodes {
		codes = append(codes, strconv.Itoa(code))
	}
	query.Set("status_codes", strings.Join(codes, ","))
	return strings.TrimRight(baseURL, "/") + "/api/v1/admin/ops/request-errors?" + query.Encode()
}

// dynamicMonitorErrorEventKey 返回一次请求的去重键，同一请求的重试只计一次。
func dynamicMonitorErrorEventKey(item remoteRequestError) string {
	if value := strings.TrimSpace(item.ClientRequestID); value != "" {
		return "client:" + value
	}
	if value := strings.TrimSpace(item.RequestID); value != "" {
		return "request:" + value
	}
	return ""
}

// dynamicMonitorErrorEventFrom 只保留账号级、上游最终失败的错误记录。
func dynamicMonitorErrorEventFrom(item remoteRequestError, statusSet map[int]struct{}) (dynamicMonitorErrorEvent, bool) {
	if item.AccountID <= 0 {
		return dynamicMonitorErrorEvent{}, false
	}
	if _, ok := statusSet[item.StatusCode]; !ok {
		return dynamicMonitorErrorEvent{}, false
	}
	owner := strings.ToLower(strings.TrimSpace(item.ErrorOwner))
	phase := strings.ToLower(strings.TrimSpace(item.ErrorPhase))
	if phase == "" {
		phase = strings.ToLower(strings.TrimSpace(item.Phase))
	}
	if owner == "client" || (owner != "provider" && phase != "upstream") {
		return dynamicMonitorErrorEvent{}, false
	}
	message := strings.TrimSpace(item.Message)
	if message == "" || strings.HasPrefix(strings.ToLower(message), "recovered upstream error") {
		return dynamicMonitorErrorEvent{}, false
	}
	created, ok := parseMonitorTime(item.CreatedAt)
	if !ok {
		return dynamicMonitorErrorEvent{}, false
	}
	return dynamicMonitorErrorEvent{AccountExternalID: item.AccountID, CreatedAt: created, StatusCode: item.StatusCode}, true
}

// fetchDynamicMonitorErrorEvents 分页读取一个报错组窗口内的错误明细。
// 第二个返回值为 false 表示统计不完整，调用方据此跳过自动关闭。
func (s *Service) fetchDynamicMonitorErrorEvents(ctx context.Context, baseURL, apiKey string, group DynamicMonitorErrorRuleGroup, since, until time.Time) ([]dynamicMonitorErrorEvent, bool) {
	if !group.IsEnabled() {
		return nil, false
	}
	statusSet := make(map[int]struct{}, len(group.StatusCodes))
	for _, code := range group.StatusCodes {
		statusSet[code] = struct{}{}
	}
	events := make([]dynamicMonitorErrorEvent, 0, dynamicMonitorErrorPageSize)
	seen := make(map[string]struct{})
	for page := 1; page <= dynamicMonitorMaxErrorPages; page++ {
		var data remoteRequestErrorPage
		endpoint := dynamicMonitorErrorEndpoint(baseURL, group.StatusCodes, since, until, page, dynamicMonitorErrorPageSize)
		if err := s.get(ctx, endpoint, apiKey, &data); err != nil {
			return nil, false
		}
		for _, item := range data.Items {
			event, ok := dynamicMonitorErrorEventFrom(item, statusSet)
			if !ok {
				continue
			}
			if key := dynamicMonitorErrorEventKey(item); key != "" {
				if _, done := seen[key]; done {
					continue
				}
				seen[key] = struct{}{}
			}
			events = append(events, event)
		}
		if len(data.Items) < dynamicMonitorErrorPageSize || int64(page*dynamicMonitorErrorPageSize) >= data.Total {
			return events, true
		}
	}
	return events, false
}

// dynamicMonitorErrorHits 计算本轮达到阈值的账号，命中靠前的报错组优先。
func (s *Service) dynamicMonitorErrorHits(ctx context.Context, station *storage.RelayStation, config DynamicMonitorConfig, state DynamicMonitorState, accounts []storage.RelayAccount) map[int64]dynamicMonitorErrorHit {
	if !slices.ContainsFunc(config.ErrorGroups, func(group DynamicMonitorErrorRuleGroup) bool { return group.IsEnabled() && len(group.StatusCodes) > 0 }) {
		return nil
	}
	schedulable := make(map[int64]struct{}, len(accounts))
	for _, account := range accounts {
		if relayAccountSchedulable(account) && !slices.Contains(config.ProtectedAccountExternalIDs, account.ExternalID) {
			schedulable[account.ExternalID] = struct{}{}
		}
	}
	if len(schedulable) == 0 {
		return nil
	}
	apiKey, err := s.cipher.Decrypt(station.APIKeyCipher)
	if err != nil {
		return nil
	}
	now := time.Now().UTC()
	hits := make(map[int64]dynamicMonitorErrorHit)
	for index, group := range config.ErrorGroups {
		if !group.IsEnabled() || len(group.StatusCodes) == 0 {
			continue
		}
		since := now.Add(-time.Duration(group.LookbackMinutes) * time.Minute)
		events, complete := s.fetchDynamicMonitorErrorEvents(ctx, station.BaseURL, apiKey, group, since, now)
		if !complete {
			continue
		}
		counts := countDynamicMonitorErrorEvents(events, schedulable, state.Judged)
		for externalID, count := range counts {
			if _, done := hits[externalID]; done {
				continue
			}
			if count < int64(group.Threshold) {
				continue
			}
			hits[externalID] = dynamicMonitorErrorHit{
				RuleGroupName:   group.Label(index),
				RuleGroupID:     group.ID,
				StatusCodes:     append([]int(nil), group.StatusCodes...),
				Count:           count,
				WindowMinutes:   group.LookbackMinutes,
				AllowAutoOpen:   group.AllowAutoOpen,
				AutoOpenMinutes: group.AutoOpenMinutes,
			}
		}
	}
	return hits
}

// countDynamicMonitorErrorEvents 按账号统计窗口内的错误数。
// 只统计可调度账号，且忽略判定边界（关闭/重开时间）及之前的记录，
// 避免账号刚被开启就被同一批旧错误再次关闭。
func countDynamicMonitorErrorEvents(events []dynamicMonitorErrorEvent, schedulable map[int64]struct{}, judged map[int64]time.Time) map[int64]int64 {
	counts := make(map[int64]int64)
	for _, event := range events {
		if _, ok := schedulable[event.AccountExternalID]; !ok {
			continue
		}
		if boundary := judged[event.AccountExternalID]; !boundary.IsZero() && !event.CreatedAt.After(boundary) {
			continue
		}
		counts[event.AccountExternalID]++
	}
	return counts
}

// autoOpenDeadline 按配置计算自动开启时间，未启用时返回 nil。
func autoOpenDeadline(now time.Time, allow bool, minutes int) *time.Time {
	if !allow || minutes <= 0 {
		return nil
	}
	value := now.UTC().Add(time.Duration(minutes) * time.Minute)
	return &value
}
