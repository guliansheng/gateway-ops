package relay

import (
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestClassifyGroupMonitor(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	success := func(offset time.Duration) monitorEvent {
		return monitorEvent{success: true, createdAt: now.Add(offset)}
	}
	failure := func(offset time.Duration) monitorEvent {
		return monitorEvent{success: false, createdAt: now.Add(offset)}
	}
	tests := []struct {
		name     string
		group    string
		complete bool
		events   []monitorEvent
		want     string
	}{
		{name: "disabled remote group", group: "inactive", complete: true, want: "disabled"},
		{name: "source unavailable", group: "active", complete: false, events: []monitorEvent{success(0)}, want: "unknown"},
		{name: "no calls", group: "active", complete: true, want: "idle"},
		{name: "healthy", group: "active", complete: true, events: []monitorEvent{success(0), success(-time.Minute)}, want: "available"},
		{name: "one recent failure", group: "active", complete: true, events: []monitorEvent{failure(0), success(-time.Minute)}, want: "degraded"},
		{name: "two recent failures", group: "active", complete: true, events: []monitorEvent{failure(0), failure(-time.Minute), success(-2 * time.Minute)}, want: "degraded"},
		{name: "three recent failures", group: "active", complete: true, events: []monitorEvent{failure(0), failure(-time.Minute), failure(-2 * time.Minute), success(-3 * time.Minute)}, want: "unavailable"},
		{name: "restored by latest success", group: "active", complete: true, events: []monitorEvent{success(0), failure(-time.Minute), failure(-2 * time.Minute)}, want: "available"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyGroupMonitor(test.group, test.complete, test.events); got != test.want {
				t.Fatalf("status = %q, want %q", got, test.want)
			}
		})
	}
}

func TestErrorMonitorEventsOnlyIncludesUserVisibleFinalFailures(t *testing.T) {
	items := []remoteRequestError{
		{CreatedAt: "2026-08-18T12:00:00Z", Phase: "upstream", ErrorOwner: "provider", StatusCode: 502, Message: "Upstream request failed", GroupID: 28, RequestedModel: "gpt-5.6"},
		{CreatedAt: "2026-08-18T12:00:30Z", ErrorPhase: "upstream", ErrorOwner: "provider", StatusCode: 429, Message: "Recovered upstream error 429: rate limited", GroupID: 28, RequestedModel: "gpt-5.6"},
		{CreatedAt: "2026-08-18T12:00:20Z", ErrorPhase: "upstream", ErrorOwner: "provider", StatusCode: 499, GroupID: 28, RequestedModel: "gpt-5.6"},
		{CreatedAt: "2026-08-18T12:00:10Z", ErrorPhase: "upstream", ErrorOwner: "provider", GroupID: 28, RequestedModel: "gpt-5.6"},
		{CreatedAt: "2026-08-18T11:59:00Z", Phase: "client", ErrorOwner: "client", GroupID: 28, RequestedModel: "gpt-5.6"},
		{CreatedAt: "2026-08-18T11:58:00Z", Phase: "routing", ErrorOwner: "system", StatusCode: 503, GroupID: 28, RequestedModel: "gpt-5.6"},
	}
	got := errorMonitorEvents(items, 28)
	if len(got) != 1 || got[0].success || got[0].model != "gpt-5.6" {
		t.Fatalf("filtered upstream events = %#v", got)
	}
	if got[0].failureReason != monitorFailureUpstream {
		t.Fatalf("failure reason = %q, want upstream response error", got[0].failureReason)
	}
}

func TestMonitorFailureReason(t *testing.T) {
	tests := []struct {
		name string
		item remoteRequestError
		want string
	}{
		{name: "model unavailable", item: remoteRequestError{StatusCode: 404, Message: "The model gpt-5.6-sol does not exist or you do not have access to it"}, want: monitorFailureModelUnavailable},
		{name: "rate limit", item: remoteRequestError{StatusCode: 429, Message: "rate limit exceeded"}, want: "请求频率或并发已达上限"},
		{name: "quota", item: remoteRequestError{StatusCode: 429, Message: "insufficient quota"}, want: "可用额度不足"},
		{name: "credentials", item: remoteRequestError{StatusCode: 401, Message: "invalid api key"}, want: "凭证或权限异常"},
		{name: "bad request", item: remoteRequestError{StatusCode: 400, Message: "invalid request"}, want: "请求参数不符合要求"},
		{name: "temporary service failure", item: remoteRequestError{StatusCode: 502, Message: "upstream temporarily unavailable"}, want: monitorFailureUpstream},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := monitorFailureReason(test.item); got != test.want {
				t.Fatalf("reason = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFilterRecoveredMonitorFailures(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	usage := []monitorEvent{{success: true, requestID: "client:completed", createdAt: now}}
	failures := []monitorEvent{
		{requestID: "internal-attempt", clientRequestID: "completed", createdAt: now.Add(-2 * time.Second)},
		{requestID: "failed-attempt-1", clientRequestID: "failed", createdAt: now.Add(-3 * time.Second)},
		{requestID: "failed-attempt-2", clientRequestID: "failed", createdAt: now.Add(-time.Second)},
		{createdAt: now.Add(-4 * time.Second)},
	}
	got := filterRecoveredMonitorFailures(usage, failures)
	if len(got) != 2 {
		t.Fatalf("filtered failures = %#v, want one correlated failure and one uncorrelated failure", got)
	}
	for _, event := range got {
		if event.clientRequestID == "completed" {
			t.Fatal("recovered request was kept")
		}
	}
	var latest time.Time
	for _, event := range got {
		if event.clientRequestID == "failed" {
			latest = event.createdAt
		}
	}
	if !latest.Equal(now.Add(-time.Second)) {
		t.Fatalf("kept failure timestamp = %s, want latest error", latest)
	}
}

func TestBuildPublicGroupMonitorGroupSummarizesMainFailureReason(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	group := storage.RelayGroup{ExternalID: 28, Name: "GPT", Description: "低价 GPT 分组", Platform: "openai", RateMultiplier: 0.3, Status: "active", MonitorEnabled: true}
	failures := []monitorEvent{
		{createdAt: now, model: "gpt-5.6-luna", failureReason: monitorFailureModelUnavailable},
		{createdAt: now.Add(-time.Second), model: "gpt-5.6-luna", failureReason: monitorFailureUpstream},
		{createdAt: now.Add(-2 * time.Second), model: "gpt-5.6-luna", failureReason: monitorFailureModelUnavailable},
	}
	got := buildPublicGroupMonitorGroup(group, nil, failures, true, now)
	if got.FailureSummary != "报错主要原因：gpt-5.6-luna 模型当前不可用（2 次）" {
		t.Fatalf("failure summary = %q", got.FailureSummary)
	}
	if got.Description != group.Description || got.Platform != group.Platform || got.RateMultiplier != group.RateMultiplier {
		t.Fatalf("group metadata = %#v", got)
	}
}

func TestBuildPublicGroupMonitorGroupInfersUnavailableModelFromDistribution(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	group := storage.RelayGroup{ExternalID: 28, Name: "GPT", Status: "active", MonitorEnabled: true}
	usage := []monitorEvent{
		{createdAt: now.Add(-4 * time.Second), success: true, model: "gpt-5.6"},
		{createdAt: now.Add(-5 * time.Second), success: true, model: "gpt-5.6"},
	}
	failures := []monitorEvent{
		{createdAt: now, model: "gpt-5.6-luna", failureReason: monitorFailureUpstream},
		{createdAt: now.Add(-time.Second), model: "gpt-5.6-luna", failureReason: monitorFailureUpstream},
		{createdAt: now.Add(-2 * time.Second), model: "gpt-5.6-luna", failureReason: monitorFailureUpstream},
	}
	got := buildPublicGroupMonitorGroup(group, usage, failures, true, now)
	if got.FailureSummary != "报错主要原因：gpt-5.6-luna 模型当前不可用（3 次）" {
		t.Fatalf("failure summary = %q", got.FailureSummary)
	}
}

func TestBuildPublicGroupMonitorGroupKeepsGenericReasonWhenModelsDoNotExplainFailures(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	group := storage.RelayGroup{ExternalID: 28, Name: "GPT", Status: "active", MonitorEnabled: true}
	usage := []monitorEvent{{createdAt: now.Add(-time.Second), success: true, model: "gpt-5.6"}}
	failures := []monitorEvent{{createdAt: now, model: "gpt-5.6-luna", failureReason: monitorFailureUpstream}}
	got := buildPublicGroupMonitorGroup(group, usage, failures, true, now)
	if got.FailureSummary != "报错主要原因：上游响应异常（1 次）" {
		t.Fatalf("failure summary = %q", got.FailureSummary)
	}
}
