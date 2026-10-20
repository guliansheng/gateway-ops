package relay

import (
	"net/url"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

// 报错组按具体状态码配置：去重排序，非法范围被丢弃，缺省值补齐。
func TestDynamicMonitorErrorGroupNormalizeDedupesStatusCodes(t *testing.T) {
	group := DynamicMonitorErrorRuleGroup{
		Name:            "  502  ",
		StatusCodes:     []int{503, 502, 503, 399, 600, 0},
		Threshold:       0,
		LookbackMinutes: 0,
		AutoOpenMinutes: -1,
	}.Normalize()

	if group.Name != "502" {
		t.Fatalf("名称未去空白: %q", group.Name)
	}
	if len(group.StatusCodes) != 2 || group.StatusCodes[0] != 502 || group.StatusCodes[1] != 503 {
		t.Fatalf("状态码 = %v，期望 [502 503]", group.StatusCodes)
	}
	if group.Threshold != dynamicMonitorDefaultErrorThreshold {
		t.Fatalf("阈值 = %d，期望默认 %d", group.Threshold, dynamicMonitorDefaultErrorThreshold)
	}
	if group.LookbackMinutes != dynamicMonitorDefaultErrorLookback {
		t.Fatalf("统计窗口 = %d，期望默认 %d", group.LookbackMinutes, dynamicMonitorDefaultErrorLookback)
	}
	if group.AutoOpenMinutes != 0 {
		t.Fatalf("负数自动开启时间未归零: %d", group.AutoOpenMinutes)
	}
}

// 报错组只接受 400-599 的具体状态码；填 4xx/5xx 范围视为非法。
func TestDynamicMonitorErrorGroupValidation(t *testing.T) {
	valid := DynamicMonitorConfig{
		Enabled: true,
		ErrorGroups: []DynamicMonitorErrorRuleGroup{{
			Name: "上游 5xx", StatusCodes: []int{502, 503}, Threshold: 3, LookbackMinutes: 10,
		}},
	}
	if err := valid.Normalize().Validate(); err != nil {
		t.Fatalf("合法报错组报错: %v", err)
	}

	for _, test := range []struct {
		name  string
		group DynamicMonitorErrorRuleGroup
	}{
		{name: "范围值", group: DynamicMonitorErrorRuleGroup{StatusCodes: []int{4}, Threshold: 3, LookbackMinutes: 10}},
		{name: "越界状态码", group: DynamicMonitorErrorRuleGroup{StatusCodes: []int{600}, Threshold: 3, LookbackMinutes: 10}},
		{name: "阈值过大", group: DynamicMonitorErrorRuleGroup{StatusCodes: []int{502}, Threshold: 1001, LookbackMinutes: 10}},
		{name: "窗口过大", group: DynamicMonitorErrorRuleGroup{StatusCodes: []int{502}, Threshold: 3, LookbackMinutes: 10081}},
		{name: "自动开启越界", group: DynamicMonitorErrorRuleGroup{StatusCodes: []int{502}, Threshold: 3, LookbackMinutes: 10, AutoOpenMinutes: 10081}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := DynamicMonitorConfig{Enabled: true, ErrorGroups: []DynamicMonitorErrorRuleGroup{test.group}}
			if err := config.Normalize().Validate(); err == nil {
				t.Fatalf("非法报错组未报错: %+v", test.group)
			}
		})
	}

	// 没有有效状态码的组会被丢弃，启用状态下应报“至少一个规则组”。
	empty := DynamicMonitorConfig{Enabled: true, ErrorGroups: []DynamicMonitorErrorRuleGroup{{Name: "空"}}}
	if err := empty.Normalize().Validate(); err == nil {
		t.Fatal("空报错组未报错")
	}
}

// 报错组对所有账号生效，不要求账号先加入监测规则组。
func TestPlanDynamicMonitorClosesAllAccountsOnErrorRuleHit(t *testing.T) {
	config := DynamicMonitorConfig{
		Enabled: true,
		Groups:  []DynamicMonitorRuleGroup{ruleGroup(1, 3, 11)},
		ErrorGroups: []DynamicMonitorErrorRuleGroup{{
			Name: "上游 5xx", StatusCodes: []int{502, 503}, Threshold: 2, LookbackMinutes: 10,
		}},
	}.Normalize()
	accounts := []storage.RelayAccount{
		{ExternalID: 11, ID: 1, Name: "规则组账号", Schedulable: true, Status: "active"},
		{ExternalID: 12, ID: 2, Name: "未参与规则组", Schedulable: true, Status: "active"},
	}
	hits := map[int64]dynamicMonitorErrorHit{
		12: {RuleGroupName: "上游 5xx", StatusCodes: []int{502, 503}, Count: 4, WindowMinutes: 10},
	}

	plan := planDynamicMonitorWithErrors(config, DynamicMonitorState{}, accounts, nil, nil, hits)
	if len(plan.Close) != 1 || plan.Close[0] != 12 {
		t.Fatalf("关闭名单 = %v，期望 [12]", plan.Close)
	}
	if plan.CloseReasons[12] != dynamicMonitorReasonError {
		t.Fatalf("关闭原因 = %q，期望报错规则组", plan.CloseReasons[12])
	}
	if got := plan.CloseErrorHits[12]; got.Count != 4 || len(got.StatusCodes) != 2 {
		t.Fatalf("命中信息 = %+v", got)
	}
}

// 分组保底不受自动开启开关影响：低于下限时仍要补开已关闭账号。
func TestPlanDynamicMonitorKeepsGroupFloorWithoutAutoOpen(t *testing.T) {
	config := DynamicMonitorConfig{
		Enabled:             true,
		Groups:              []DynamicMonitorRuleGroup{ruleGroup(1, 3, 11)},
		MinAccountsPerGroup: 2,
	}.Normalize()
	accounts := []storage.RelayAccount{
		{ExternalID: 11, ID: 1, Name: "可用", Schedulable: true, Status: "active"},
		{ExternalID: 12, ID: 2, Name: "被规则关闭", Schedulable: false, Status: "active"},
	}
	groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{
		{RelayGroupID: 1, RelayAccountID: 1},
		{RelayGroupID: 1, RelayAccountID: 2},
	}
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{
		{AccountExternalID: 12, ClosedAt: time.Now().UTC().Add(-time.Hour)},
	}}

	plan := planDynamicMonitor(config, state, accounts, groups, links)
	if len(plan.Reopen) != 1 || plan.Reopen[0] != 12 {
		t.Fatalf("补开名单 = %v，期望 [12]", plan.Reopen)
	}
}

func TestAutoOpenDeadlineHonorsPerRuleConfig(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if autoOpenDeadline(now, false, 30) != nil {
		t.Fatal("未允许动态开启时不应产生时间")
	}
	if autoOpenDeadline(now, true, 0) != nil {
		t.Fatal("0 分钟应视为不启用")
	}
	deadline := autoOpenDeadline(now, true, 30)
	if deadline == nil || !deadline.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("自动开启时间 = %v", deadline)
	}
}

// 旧版关闭记录没有 AutoOpenAt，回落到整站配置；报错组记录不回落。
func TestDynamicMonitorClosureDeadlineFallsBackForLegacyRecords(t *testing.T) {
	closedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	config := DynamicMonitorConfig{AllowAutoOpen: true, AutoOpenMinutes: 10}

	legacy := dynamicMonitorClosureDeadline(DynamicMonitorClosure{ClosedAt: closedAt}, config)
	if legacy == nil || !legacy.Equal(closedAt.Add(10*time.Minute)) {
		t.Fatalf("旧版记录自动开启时间 = %v", legacy)
	}
	if got := dynamicMonitorClosureDeadline(DynamicMonitorClosure{ClosedAt: closedAt, RuleCategory: dynamicMonitorRuleCategoryError}, config); got != nil {
		t.Fatalf("报错组记录不应回落到整站配置: %v", got)
	}
}

// 只统计账号级、上游最终失败且状态码匹配的错误。
func TestDynamicMonitorErrorEventFromFiltersProviderFailures(t *testing.T) {
	statusSet := map[int]struct{}{502: {}, 503: {}}
	valid := remoteRequestError{
		AccountID: 7, StatusCode: 502, ErrorOwner: "provider", ErrorPhase: "upstream",
		Message: "bad gateway", CreatedAt: "2026-01-01T00:00:00Z",
	}
	if _, ok := dynamicMonitorErrorEventFrom(valid, statusSet); !ok {
		t.Fatal("合法的上游失败记录被丢弃")
	}

	for _, test := range []struct {
		name string
		item remoteRequestError
	}{
		{name: "无账号", item: remoteRequestError{StatusCode: 502, ErrorOwner: "provider", Message: "x", CreatedAt: "2026-01-01T00:00:00Z"}},
		{name: "状态码不匹配", item: remoteRequestError{AccountID: 7, StatusCode: 429, ErrorOwner: "provider", Message: "x", CreatedAt: "2026-01-01T00:00:00Z"}},
		{name: "客户端错误", item: remoteRequestError{AccountID: 7, StatusCode: 502, ErrorOwner: "client", Message: "x", CreatedAt: "2026-01-01T00:00:00Z"}},
		{name: "已恢复重试", item: remoteRequestError{AccountID: 7, StatusCode: 502, ErrorOwner: "provider", Message: "Recovered upstream error 502", CreatedAt: "2026-01-01T00:00:00Z"}},
		{name: "空消息", item: remoteRequestError{AccountID: 7, StatusCode: 502, ErrorOwner: "provider", CreatedAt: "2026-01-01T00:00:00Z"}},
		{name: "时间非法", item: remoteRequestError{AccountID: 7, StatusCode: 502, ErrorOwner: "provider", Message: "x", CreatedAt: "not-a-time"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := dynamicMonitorErrorEventFrom(test.item, statusSet); ok {
				t.Fatalf("不应计入: %+v", test.item)
			}
		})
	}
}

func TestDynamicMonitorErrorEndpointUsesConcreteStatusCodes(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	endpoint := dynamicMonitorErrorEndpoint("https://relay.example/", []int{502, 503}, since, until, 2, 500)

	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("解析地址失败: %v", err)
	}
	if parsed.Path != "/api/v1/admin/ops/request-errors" {
		t.Fatalf("路径 = %q", parsed.Path)
	}
	query := parsed.Query()
	if query.Get("status_codes") != "502,503" {
		t.Fatalf("status_codes = %q", query.Get("status_codes"))
	}
	if query.Get("page") != "2" || query.Get("page_size") != "500" {
		t.Fatalf("分页参数 = %q/%q", query.Get("page"), query.Get("page_size"))
	}
	if query.Get("start_time") != since.Format(time.RFC3339) || query.Get("end_time") != until.Format(time.RFC3339) {
		t.Fatalf("时间窗口 = %q 到 %q", query.Get("start_time"), query.Get("end_time"))
	}
}

// 只统计可调度账号，且忽略判定边界（关闭/重开时间）及之前的记录。
func TestCountDynamicMonitorErrorEventsRespectsBoundary(t *testing.T) {
	boundary := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	events := []dynamicMonitorErrorEvent{
		{AccountExternalID: 1, CreatedAt: boundary.Add(-time.Minute)},
		{AccountExternalID: 1, CreatedAt: boundary},
		{AccountExternalID: 1, CreatedAt: boundary.Add(time.Minute)},
		{AccountExternalID: 1, CreatedAt: boundary.Add(2 * time.Minute)},
		{AccountExternalID: 2, CreatedAt: boundary.Add(time.Minute)},
		{AccountExternalID: 3, CreatedAt: boundary.Add(time.Minute)},
	}
	schedulable := map[int64]struct{}{1: {}, 2: {}}
	judged := map[int64]time.Time{1: boundary}

	counts := countDynamicMonitorErrorEvents(events, schedulable, judged)
	if counts[1] != 2 {
		t.Fatalf("账号 1 计数 = %d，期望 2（只统计边界之后）", counts[1])
	}
	if counts[2] != 1 {
		t.Fatalf("账号 2 计数 = %d，期望 1（无边界则全窗口统计）", counts[2])
	}
	if _, ok := counts[3]; ok {
		t.Fatal("不可调度账号不应计入")
	}
}
