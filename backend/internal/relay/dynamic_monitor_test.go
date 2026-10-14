package relay

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// latencyBody 生成 count 条首字延迟样本（从 at 开始往前每分钟一条），时间倒序存储。
func latencyBody(t *testing.T, firstTokenMS int64, count int, at time.Time) string {
	t.Helper()
	samples := make([]storage.RelayLatencySample, 0, count)
	for i := 0; i < count; i++ {
		samples = append(samples, storage.RelayLatencySample{
			FirstTokenMS: firstTokenMS,
			CreatedAt:    at.Add(-time.Duration(i) * time.Minute),
		})
	}
	body, err := json.Marshal(samples)
	if err != nil {
		t.Fatalf("编码延迟样本失败: %v", err)
	}
	return string(body)
}

// ruleGroup 是测试里构造规则组的简写。
func ruleGroup(thresholdSeconds float64, consecutive int, ids ...int64) DynamicMonitorRuleGroup {
	return DynamicMonitorRuleGroup{
		FirstTokenSeconds:  thresholdSeconds,
		ConsecutiveCount:   consecutive,
		AccountExternalIDs: ids,
	}
}

func TestConsecutiveFirstTokenOverCountsNewestPrefix(t *testing.T) {
	base := time.Now().UTC()
	samples := []storage.RelayLatencySample{
		// 样本顺序故意打乱，函数应按 CreatedAt 倒序统计。
		{FirstTokenMS: 900, CreatedAt: base.Add(-2 * time.Minute)},
		{FirstTokenMS: 5000, CreatedAt: base},
		{FirstTokenMS: 5000, CreatedAt: base.Add(-time.Minute)},
		{FirstTokenMS: 100, CreatedAt: base.Add(-3 * time.Minute)},
	}
	if got := consecutiveFirstTokenOver(samples, 1000); got != 2 {
		t.Fatalf("连续超标次数 = %d，期望 2", got)
	}
	if got := consecutiveFirstTokenOver(samples, 6000); got != 0 {
		t.Fatalf("低于阈值时连续超标次数 = %d，期望 0", got)
	}
	if got := consecutiveFirstTokenOver(nil, 1000); got != 0 {
		t.Fatalf("空样本连续超标次数 = %d，期望 0", got)
	}
	if got := consecutiveFirstTokenOver(samples, 0); got != 0 {
		t.Fatalf("阈值非法时连续超标次数 = %d，期望 0", got)
	}
}

func TestGroupNormalizeDefaultsAndDedup(t *testing.T) {
	group := DynamicMonitorRuleGroup{
		Name:               "  高敏  ",
		AccountExternalIDs: []int64{30, 10, 30, 0, -1, 10},
	}.Normalize()

	if group.FirstTokenSeconds != dynamicMonitorDefaultThresholdSeconds {
		t.Fatalf("首字延迟阈值 = %v，期望默认 %v", group.FirstTokenSeconds, dynamicMonitorDefaultThresholdSeconds)
	}
	if group.ConsecutiveCount != dynamicMonitorDefaultConsecutive {
		t.Fatalf("连续超标次数 = %d，期望默认 %d", group.ConsecutiveCount, dynamicMonitorDefaultConsecutive)
	}
	if group.Name != "高敏" {
		t.Fatalf("规则组名称未去空白: %q", group.Name)
	}
	want := []int64{10, 30}
	if len(group.AccountExternalIDs) != len(want) {
		t.Fatalf("参与账号 = %v，期望 %v", group.AccountExternalIDs, want)
	}
	for i, id := range want {
		if group.AccountExternalIDs[i] != id {
			t.Fatalf("参与账号未去重排序: %v，期望 %v", group.AccountExternalIDs, want)
		}
	}
}

func TestConfigNormalizeClampsGlobalAndDropsEmptyGroups(t *testing.T) {
	config := DynamicMonitorConfig{
		MinAccountsPerGroup: -5,
		AutoOpenMinutes:     -1,
		Groups: []DynamicMonitorRuleGroup{
			ruleGroup(12, 2, 11),
			// 没有账号的组应被丢弃。
			ruleGroup(30, 5),
		},
	}.Normalize()

	if config.MinAccountsPerGroup != 0 || config.AutoOpenMinutes != 0 {
		t.Fatalf("负数通用配置未归零: %+v", config)
	}
	if len(config.Groups) != 1 {
		t.Fatalf("空规则组未清理: %+v", config.Groups)
	}
	if config.Groups[0].AccountExternalIDs[0] != 11 {
		t.Fatalf("规则组账号错误: %+v", config.Groups[0])
	}
}

// 同一账号出现在多个规则组时，应只归属第一个提到它的组。
func TestConfigNormalizeClaimsAccountToFirstGroupOnly(t *testing.T) {
	config := DynamicMonitorConfig{
		Groups: []DynamicMonitorRuleGroup{
			ruleGroup(5, 2, 11, 12),
			ruleGroup(30, 5, 12, 13),
		},
	}.Normalize()

	if len(config.Groups) != 2 {
		t.Fatalf("规则组数量 = %d，期望 2", len(config.Groups))
	}
	first := config.Groups[0].AccountExternalIDs
	second := config.Groups[1].AccountExternalIDs
	if len(first) != 2 || first[0] != 11 || first[1] != 12 {
		t.Fatalf("第一组账号 = %v，期望 [11 12]", first)
	}
	// 12 已被第一组占用，第二组只剩 13。
	if len(second) != 1 || second[0] != 13 {
		t.Fatalf("第二组账号 = %v，期望 [13]", second)
	}
}

func TestDynamicMonitorConfigValidate(t *testing.T) {
	valid := DynamicMonitorConfig{
		Enabled:             true,
		Groups:              []DynamicMonitorRuleGroup{ruleGroup(12, 3, 1)},
		AllowAutoOpen:       true,
		AutoOpenMinutes:     30,
		MinAccountsPerGroup: 2,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法配置报错: %v", err)
	}

	noGroups := valid
	noGroups.Groups = nil
	if err := noGroups.Validate(); err == nil {
		t.Fatal("启用但没有规则组时应报错")
	}

	badThreshold := valid
	badThreshold.Groups = []DynamicMonitorRuleGroup{ruleGroup(0.01, 3, 1)}
	if err := badThreshold.Validate(); err == nil {
		t.Fatal("阈值过小时应报错")
	}

	badConsecutive := valid
	badConsecutive.Groups = []DynamicMonitorRuleGroup{ruleGroup(12, 0, 1)}
	if err := badConsecutive.Validate(); err == nil {
		t.Fatal("连续次数非法时应报错")
	}

	emptyGroup := valid
	emptyGroup.Groups = []DynamicMonitorRuleGroup{{FirstTokenSeconds: 12, ConsecutiveCount: 1}}
	if err := emptyGroup.Validate(); err == nil {
		t.Fatal("规则组没有账号时应报错")
	}

	autoOpenWithoutPlan := valid
	autoOpenWithoutPlan.AutoOpenMinutes = 0
	autoOpenWithoutPlan.MinAccountsPerGroup = 0
	if err := autoOpenWithoutPlan.Validate(); err == nil {
		t.Fatal("允许动态开启但既无超时时间也无最少账号数时应报错")
	}

	disabled := DynamicMonitorConfig{}
	if err := disabled.Validate(); err != nil {
		t.Fatalf("关闭状态的空配置应合法: %v", err)
	}
}

// 旧版单套规则结构应迁移成一个默认规则组，阈值从毫秒换算成秒。
func TestParseDynamicMonitorConfigMigratesLegacyShape(t *testing.T) {
	raw := `{"enabled":true,"account_external_ids":[21,22],"first_token_threshold_ms":12000,"consecutive_count":2,"min_accounts_per_group":6,"allow_auto_open":true,"auto_open_minutes":10}`
	config := ParseDynamicMonitorConfig(raw)

	if !config.Enabled || !config.AllowAutoOpen {
		t.Fatalf("通用开关未保留: %+v", config)
	}
	if config.MinAccountsPerGroup != 6 || config.AutoOpenMinutes != 10 {
		t.Fatalf("通用配置未保留: %+v", config)
	}
	if len(config.Groups) != 1 {
		t.Fatalf("旧结构未迁移成一个规则组: %+v", config.Groups)
	}
	group := config.Groups[0]
	if group.FirstTokenSeconds != 12 {
		t.Fatalf("阈值换算错误 = %v，期望 12 秒", group.FirstTokenSeconds)
	}
	if group.ConsecutiveCount != 2 {
		t.Fatalf("连续次数 = %d，期望 2", group.ConsecutiveCount)
	}
	if len(group.AccountExternalIDs) != 2 {
		t.Fatalf("账号未保留: %v", group.AccountExternalIDs)
	}
}

// 新结构与空输入都要返回非 nil 的组切片，避免前端读 length 报错。
func TestParseDynamicMonitorConfigReturnsNonNilGroups(t *testing.T) {
	for _, raw := range []string{"", "{}"} {
		config := ParseDynamicMonitorConfig(raw)
		if config.Groups == nil {
			t.Fatalf("raw=%q 时 Groups 为 nil", raw)
		}
	}
}

func TestPlanDynamicMonitorAppliesPerGroupThreshold(t *testing.T) {
	base := time.Now().UTC()
	config := DynamicMonitorConfig{
		Enabled: true,
		// 账号 11 用严格规则（1 秒 / 2 次），账号 12 用宽松规则（30 秒 / 2 次）。
		Groups: []DynamicMonitorRuleGroup{
			ruleGroup(1, 2, 11),
			ruleGroup(30, 2, 12),
		},
		AllowAutoOpen:       true,
		MinAccountsPerGroup: 2,
	}.Normalize()

	accounts := []storage.RelayAccount{
		// 首字 5 秒：超过本组 1 秒阈值，连续 3 次 → 关闭。
		{ExternalID: 11, ID: 1, Name: "严格组超标", Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 3, base)},
		// 首字 5 秒：未超过本组 30 秒阈值 → 保留。同一延迟在另一组会被关闭。
		{ExternalID: 12, ID: 2, Name: "宽松组正常", Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 3, base)},
		// 未参与任何规则组，即使超标也不关闭。
		{ExternalID: 13, ID: 3, Name: "未参与", Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 3, base)},
		// 已关闭的参与账号不重复关闭。
		{ExternalID: 14, ID: 4, Name: "已关闭", Schedulable: false, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 3, base)},
	}
	groups := []storage.RelayGroup{{ID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{
		{RelayGroupID: 1, RelayAccountID: 1},
		{RelayGroupID: 1, RelayAccountID: 2},
	}

	plan := planDynamicMonitor(config, DynamicMonitorState{}, accounts, groups, links)
	if len(plan.Close) != 1 || plan.Close[0] != 11 {
		t.Fatalf("关闭名单 = %v，期望 [11]", plan.Close)
	}
	// 分组里 11 即将关闭，可用只剩 12（< 2），但没有任何被规则关闭的账号，无法补开。
	if len(plan.Reopen) != 0 {
		t.Fatalf("补开名单 = %v，期望为空", plan.Reopen)
	}
}

func TestPlanDynamicMonitorReopensToMeetMinAccounts(t *testing.T) {
	base := time.Now().UTC()
	config := DynamicMonitorConfig{
		Enabled:             true,
		Groups:              []DynamicMonitorRuleGroup{ruleGroup(1, 2, 11)},
		AllowAutoOpen:       true,
		MinAccountsPerGroup: 2,
	}.Normalize()
	accounts := []storage.RelayAccount{
		{ExternalID: 11, ID: 1, Name: "唯一可用", Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 100, 3, base)},
		{ExternalID: 12, ID: 2, Name: "被规则关闭-早", Schedulable: false, Status: "active"},
		{ExternalID: 13, ID: 3, Name: "被规则关闭-晚", Schedulable: false, Status: "active"},
		{ExternalID: 14, ID: 4, Name: "手动关闭不补开", Schedulable: false, Status: "active"},
	}
	groups := []storage.RelayGroup{{ID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{
		{RelayGroupID: 1, RelayAccountID: 1},
		{RelayGroupID: 1, RelayAccountID: 2},
		{RelayGroupID: 1, RelayAccountID: 3},
		{RelayGroupID: 1, RelayAccountID: 4},
	}
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{
		{AccountExternalID: 12, AccountName: "被规则关闭-早", ClosedAt: base.Add(-2 * time.Hour)},
		{AccountExternalID: 13, AccountName: "被规则关闭-晚", ClosedAt: base.Add(-time.Hour)},
	}}

	plan := planDynamicMonitor(config, state, accounts, groups, links)
	if len(plan.Close) != 0 {
		t.Fatalf("关闭名单 = %v，期望为空", plan.Close)
	}
	// 只有 11 可用，需要补开 1 个，优先补关闭时间更早的 12。
	if len(plan.Reopen) != 1 || plan.Reopen[0] != 12 {
		t.Fatalf("补开名单 = %v，期望 [12]", plan.Reopen)
	}
	// 未触发补开的关闭记录应保留在状态里。
	if len(plan.State.ClosedAccounts) != 2 {
		t.Fatalf("清理后状态 = %+v，期望保留 2 条", plan.State)
	}
}

func TestPlanDynamicMonitorDropsStaleClosures(t *testing.T) {
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{
		{AccountExternalID: 11, ClosedAt: time.Now().UTC()},
		{AccountExternalID: 99, ClosedAt: time.Now().UTC()},
	}}
	// 账号 11 已被手动开启、账号 99 已删除：两条记录都不再跟踪。
	accounts := []storage.RelayAccount{{ExternalID: 11, ID: 1, Schedulable: true, Status: "active"}}

	plan := planDynamicMonitor(DynamicMonitorConfig{}, state, accounts, nil, nil)
	if len(plan.State.ClosedAccounts) != 0 {
		t.Fatalf("过期关闭记录未清理: %+v", plan.State.ClosedAccounts)
	}
	if len(plan.Close) != 0 || len(plan.Reopen) != 0 {
		t.Fatalf("功能关闭时不应有动作: close=%v reopen=%v", plan.Close, plan.Reopen)
	}
}

func TestDynamicMonitorConfigEnabledRuleIsCompleteAfterNormalize(t *testing.T) {
	config := DynamicMonitorConfig{
		Enabled: true,
		Groups:  []DynamicMonitorRuleGroup{{AccountExternalIDs: []int64{5, 5, 7}}},
	}.Normalize()
	if err := config.Validate(); err != nil {
		t.Fatalf("归一化后仍不合法: %v", err)
	}
	if config.Groups[0].FirstTokenSeconds != dynamicMonitorDefaultThresholdSeconds {
		t.Fatalf("阈值未使用默认值: %v", config.Groups[0].FirstTokenSeconds)
	}
}

func TestDynamicMonitorReopenedAccountRequiresFreshConsecutiveSamples(t *testing.T) {
	openedAt := time.Date(2026, 10, 13, 9, 23, 39, 0, time.UTC)
	config := DynamicMonitorConfig{Enabled: true, Groups: []DynamicMonitorRuleGroup{ruleGroup(12, 3, 2276)}}
	state := DynamicMonitorState{Judged: map[int64]time.Time{2276: openedAt}}
	for _, test := range []struct {
		name      string
		fresh     []int64
		wantClose bool
	}{
		{name: "no new traffic"},
		{name: "one new slow request", fresh: []int64{15000}},
		{name: "two new slow requests", fresh: []int64{15000, 16000}},
		{name: "three new slow requests", fresh: []int64{15000, 16000, 17000}, wantClose: true},
		{name: "healthy request resets streak", fresh: []int64{15000, 1000, 16000, 17000}},
		{name: "new streak after healthy request", fresh: []int64{1000, 15000, 16000, 17000}, wantClose: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			samples := parseLatencySamples(latencyBody(t, 20000, 3, openedAt.Add(-3*time.Hour)))
			// Requests at the boundary or without a timestamp cannot prove new traffic.
			samples = append(samples, storage.RelayLatencySample{FirstTokenMS: 20000, CreatedAt: openedAt}, storage.RelayLatencySample{FirstTokenMS: 20000})
			for i, latency := range test.fresh {
				samples = append(samples, storage.RelayLatencySample{FirstTokenMS: latency, CreatedAt: openedAt.Add(time.Duration(i+1) * time.Minute)})
			}
			body, err := json.Marshal(samples)
			if err != nil {
				t.Fatal(err)
			}
			accounts := []storage.RelayAccount{{ID: 1, ExternalID: 2276, Schedulable: true, Status: "active", LatencySamplesJSON: string(body)}}
			for round := 0; round < 3; round++ {
				plan := planDynamicMonitor(config, state, accounts, nil, nil)
				if got := len(plan.Close) == 1; got != test.wantClose {
					t.Fatalf("round %d: close = %v, wantClose = %v", round, plan.Close, test.wantClose)
				}
				if !plan.State.Judged[2276].Equal(openedAt) {
					t.Fatal("snapshot lost or advanced the reopen boundary")
				}
			}
		})
	}
}

func TestDynamicMonitorKeepsBoundaryAcrossSnapshotsAndPrunesDeletedAccounts(t *testing.T) {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	state := DynamicMonitorState{Judged: map[int64]time.Time{2276: at, 99: at}}
	accounts := []storage.RelayAccount{{ID: 1, ExternalID: 2276}}
	plan := planDynamicMonitor(DynamicMonitorConfig{}, state, accounts, nil, nil)
	if len(plan.State.Judged) != 1 || !plan.State.Judged[2276].Equal(at) {
		t.Fatalf("unexpected retained boundaries: %v", plan.State.Judged)
	}
	plan.State.Judged[2276] = at.Add(time.Hour)
	if !state.Judged[2276].Equal(at) || len(state.Judged) != 2 {
		t.Fatal("planner mutated input state")
	}
}

func TestDynamicMonitorLegacyClosurePreventsReclosingManualReopen(t *testing.T) {
	closedAt := time.Now().UTC().Add(-10 * time.Minute)
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{{AccountExternalID: 2331, ClosedAt: closedAt}}}
	accounts := []storage.RelayAccount{{ID: 1, ExternalID: 2331, Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 20000, 3, closedAt.Add(-time.Hour))}}
	config := DynamicMonitorConfig{Enabled: true, Groups: []DynamicMonitorRuleGroup{ruleGroup(12, 3, 2331)}}
	plan := planDynamicMonitor(config, state, accounts, nil, nil)
	if len(plan.Close) != 0 || len(plan.State.ClosedAccounts) != 0 {
		t.Fatalf("manual reopen reused old samples: %+v", plan)
	}
	if plan.State.Judged[2331].Before(closedAt) {
		t.Fatal("legacy closure lost its sample boundary")
	}
}

func TestDynamicMonitorBoundarySurvivesReopenAndStateRoundTrip(t *testing.T) {
	closedAt := time.Date(2026, 10, 13, 9, 13, 0, 0, time.UTC)
	openedAt := closedAt.Add(10 * time.Minute)
	config := DynamicMonitorConfig{Enabled: true, Groups: []DynamicMonitorRuleGroup{ruleGroup(12, 3, 2276)}, AllowAutoOpen: true, MinAccountsPerGroup: 1}
	account := storage.RelayAccount{ID: 1, ExternalID: 2276, Status: "active", LatencySamplesJSON: latencyBody(t, 20000, 3, closedAt.Add(-time.Minute))}
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{{AccountExternalID: 2276, ClosedAt: closedAt}}}
	groups := []storage.RelayGroup{{ID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{{RelayAccountID: 1, RelayGroupID: 1}}
	plan := planDynamicMonitor(config, state, []storage.RelayAccount{account}, groups, links)
	if len(plan.Reopen) != 1 || plan.Reopen[0] != 2276 {
		t.Fatalf("reopen = %v", plan.Reopen)
	}
	// A failed reopen keeps the closure and its boundary for the next retry.
	if len(plan.State.ClosedAccounts) != 1 || !plan.State.Judged[2276].Equal(closedAt) {
		t.Fatalf("closure was consumed before success: %+v", plan.State)
	}
	plan.State.consumeSamples(account, openedAt)
	plan.State.ClosedAccounts = removeDynamicMonitorClosure(plan.State.ClosedAccounts, account.ExternalID)
	body, err := json.Marshal(plan.State)
	if err != nil {
		t.Fatal(err)
	}
	restored := ParseDynamicMonitorState(string(body))
	account.Schedulable = true
	// New slow calls accumulate across successive snapshots, with old history present.
	for i := 0; i <= 3; i++ {
		samples := parseLatencySamples(latencyBody(t, 20000, 3, closedAt.Add(-time.Minute)))
		for j := 1; j <= i; j++ {
			samples = append(samples, storage.RelayLatencySample{FirstTokenMS: 18000, CreatedAt: openedAt.Add(time.Duration(j) * time.Minute)})
		}
		account.LatencySamplesJSON = mustLatencyJSON(t, samples)
		next := planDynamicMonitor(config, restored, []storage.RelayAccount{account}, groups, links)
		if got := len(next.Close) == 1; got != (i == 3) {
			t.Fatalf("after %d new samples: close = %v", i, next.Close)
		}
		restored = next.State
	}
}

func TestDynamicMonitorConsumedBoundaryNeverMovesBackwards(t *testing.T) {
	at := time.Now().UTC()
	account := storage.RelayAccount{ExternalID: 2331, LatencySamplesJSON: latencyBody(t, 20000, 3, at.Add(time.Minute))}
	state := DynamicMonitorState{}
	state.consumeSamples(account, at)
	state.consumeSamples(storage.RelayAccount{ExternalID: 2331}, at.Add(-time.Hour))
	if !state.Judged[2331].Equal(at.Add(time.Minute)) {
		t.Fatalf("boundary = %v", state.Judged[2331])
	}
	for _, raw := range []string{"", "[]", "invalid json"} {
		if got := dynamicMonitorSamplesAfter(raw, at); len(got) != 0 {
			t.Fatalf("invalid samples became new traffic: %v", got)
		}
	}
}

func mustLatencyJSON(t *testing.T, samples []storage.RelayLatencySample) string {
	t.Helper()
	body, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
