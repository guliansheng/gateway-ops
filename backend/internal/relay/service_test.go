package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestExtractAccountModelIDs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "strings", raw: `["gpt-5.6","gpt-5.6","claude-sonnet"]`, want: []string{"claude-sonnet", "gpt-5.6"}},
		{name: "objects", raw: `[{"id":"gpt-5.6"},{"model_id":"grok-4"}]`, want: []string{"gpt-5.6", "grok-4"}},
		{name: "nested", raw: `{"models":[{"name":"gemini-3-pro"}]}`, want: []string{"gemini-3-pro"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := extractAccountModelIDs(json.RawMessage(test.raw))
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("models = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSelectAdminGroupAPIKey(t *testing.T) {
	groupID, otherGroupID := int64(42), int64(7)
	user := func(role string) *remoteUser { return &remoteUser{Role: role} }
	items := []remoteGroupAPIKey{
		{Key: "disabled-admin", Status: "disabled", GroupID: &groupID, User: user("admin")},
		{Key: "active-user", Status: "active", GroupID: &groupID, User: user("user")},
		{Key: "wrong-group", Status: "active", GroupID: &otherGroupID, User: user("admin")},
		{Key: "  selected-admin  ", Status: "ACTIVE", GroupID: &groupID, User: user("ADMIN")},
	}
	if got := selectAdminGroupAPIKey(items, groupID); got != "selected-admin" {
		t.Fatalf("selected key = %q, want selected-admin", got)
	}
	if got := selectAdminGroupAPIKey(items[:3], groupID); got != "" {
		t.Fatalf("unexpected key selected: %q", got)
	}
}

func TestGatewayResponseHelpersHideCredential(t *testing.T) {
	const credential = "sk-sensitive"
	message := gatewayResponseMessage([]byte(`{"error":{"message":"upstream rejected sk-sensitive"}}`))
	if got := sanitizeGatewayMessage(message, credential); got != "upstream rejected [已隐藏]" {
		t.Fatalf("sanitized message = %q", got)
	}
	content := gatewayAssistantContent([]byte(`{"choices":[{"message":{"content":"OK"}}]}`))
	if content != "OK" {
		t.Fatalf("assistant content = %q", content)
	}
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain fence", raw: "```\nOK\n```", want: "OK"},
		{name: "language fence", raw: "```json\n{\"ok\":true}\n```", want: "{\"ok\":true}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := stripMarkdownCodeFence(test.raw)
			if got != test.want {
				t.Fatalf("stripped content = %q, want %q", got, test.want)
			}
		})
	}
}

func TestApplyGroupUpdateBuildsPartialPayload(t *testing.T) {
	name, description, rate, exclusive, status, monitor := "  新分组  ", "  新描述  ", 1.25, true, "inactive", true
	group := &storage.RelayGroup{Name: "旧分组", RateMultiplier: 1, Status: "active"}
	body, err := applyGroupUpdate(group, GroupUpdateInput{
		Name: &name, Description: &description, RateMultiplier: &rate, IsExclusive: &exclusive, Status: &status, MonitorEnabled: &monitor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if group.Name != "新分组" || group.Description != "新描述" || group.RateMultiplier != 1.25 || !group.IsExclusive || group.Status != "inactive" {
		t.Fatalf("updated group = %#v", group)
	}
	if group.MonitorEnabled {
		t.Fatal("exclusive group should automatically disable monitoring")
	}
	if len(body) != 5 || body["name"] != "新分组" || body["description"] != "新描述" || body["rate_multiplier"] != 1.25 || body["is_exclusive"] != true || body["status"] != "inactive" {
		t.Fatalf("payload = %#v", body)
	}
}

func TestApplyGroupUpdateAllowsManualMonitorToggleWithoutTypeChange(t *testing.T) {
	monitor := true
	group := &storage.RelayGroup{IsExclusive: true, MonitorEnabled: false}
	if _, err := applyGroupUpdate(group, GroupUpdateInput{MonitorEnabled: &monitor}); err != nil {
		t.Fatal(err)
	}
	if !group.MonitorEnabled {
		t.Fatal("manual monitor toggle was not applied")
	}
}

func TestApplyGroupUpdateEnablesMonitoringForPublicGroup(t *testing.T) {
	exclusive := false
	group := &storage.RelayGroup{IsExclusive: true, MonitorEnabled: false}
	if _, err := applyGroupUpdate(group, GroupUpdateInput{IsExclusive: &exclusive}); err != nil {
		t.Fatal(err)
	}
	if group.IsExclusive || !group.MonitorEnabled {
		t.Fatalf("public group should enable monitoring: %#v", group)
	}
}

func TestApplyGroupUpdateRejectsInvalidValues(t *testing.T) {
	blank, negative, invalidStatus := " ", -0.1, "paused"
	for _, input := range []GroupUpdateInput{
		{Name: &blank},
		{RateMultiplier: &negative},
		{Status: &invalidStatus},
	} {
		if _, err := applyGroupUpdate(&storage.RelayGroup{}, input); err == nil {
			t.Fatalf("input %#v was accepted", input)
		}
	}
}

func TestValidateGroupSortOrderUpdates(t *testing.T) {
	groups := []storage.RelayGroup{{ExternalID: 10}, {ExternalID: 20}, {ExternalID: 30}}
	valid := []GroupSortOrderUpdate{{ID: 20, SortOrder: 0}, {ID: 10, SortOrder: 10}, {ID: 30, SortOrder: 20}}
	got, err := validateGroupSortOrderUpdates(groups, valid)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[int64]int{20: 0, 10: 10, 30: 20}) {
		t.Fatalf("sort map = %#v", got)
	}

	invalid := [][]GroupSortOrderUpdate{
		valid[:2],
		{{ID: 10, SortOrder: 0}, {ID: 20, SortOrder: 10}, {ID: 40, SortOrder: 20}},
		{{ID: 10, SortOrder: 0}, {ID: 10, SortOrder: 10}, {ID: 30, SortOrder: 20}},
		{{ID: 10, SortOrder: 0}, {ID: 20, SortOrder: 0}, {ID: 30, SortOrder: 20}},
	}
	for _, updates := range invalid {
		if _, err := validateGroupSortOrderUpdates(groups, updates); err == nil {
			t.Fatalf("invalid updates accepted: %#v", updates)
		}
	}
}

func TestModelTypeSyncPair(t *testing.T) {
	tests := []struct {
		name       string
		previous   string
		next       string
		oldType    string
		newType    string
		shouldSync bool
	}{
		{name: "single type changed", previous: `["GPT"]`, next: `["claude"]`, oldType: "gpt", newType: "claude", shouldSync: true},
		{name: "new type assigned to unbound group", previous: `[]`, next: `["gpt"]`, newType: "gpt", shouldSync: true},
		{name: "type cleared", previous: `["gpt"]`, next: `[]`, oldType: "gpt", shouldSync: true},
		{name: "multi type is ambiguous", previous: `["gpt","claude"]`, next: `["gemini"]`, newType: "gemini", shouldSync: false},
		{name: "unchanged", previous: `["gpt"]`, next: `["gpt"]`, oldType: "gpt", newType: "gpt", shouldSync: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldType, newType, ok := modelTypeSyncPair(test.previous, test.next)
			if oldType != test.oldType || newType != test.newType || ok != test.shouldSync {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, %v)", oldType, newType, ok, test.oldType, test.newType, test.shouldSync)
			}
		})
	}
}

func TestUsageStatsEndpointIncludesExactRangeAndAccount(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	previousLocal := time.Local
	time.Local = location
	defer func() { time.Local = previousLocal }()
	since := time.Date(2026, 8, 10, 16, 30, 0, 0, location)
	until := time.Date(2026, 8, 11, 16, 30, 0, 0, location)
	endpoint := usageStatsEndpoint("https://relay.example/", 42, since, until)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/api/v1/admin/usage/stats" || query.Get("account_id") != "42" {
		t.Fatalf("endpoint = %s", endpoint)
	}
	if query.Get("start_date") != "2026-08-10" || query.Get("end_date") != "2026-08-11" {
		t.Fatalf("date range = %s to %s", query.Get("start_date"), query.Get("end_date"))
	}
	if query.Get("start_time") != since.Format(time.RFC3339) || query.Get("end_time") != until.Format(time.RFC3339) {
		t.Fatalf("exact range = %s to %s", query.Get("start_time"), query.Get("end_time"))
	}
}

func TestUsageTotalEndpointOmitsAccountFilter(t *testing.T) {
	since := time.Date(2026, 8, 11, 0, 0, 0, 0, time.Local)
	until := time.Date(2026, 8, 11, 16, 30, 0, 0, time.Local)
	endpoint := usageTotalEndpoint("https://relay.example/", since, until)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/api/v1/admin/usage/stats" {
		t.Fatalf("endpoint = %s", endpoint)
	}
	if _, ok := query["account_id"]; ok {
		t.Fatalf("aggregate endpoint contains account_id: %s", endpoint)
	}
	if query.Get("start_time") != since.Format(time.RFC3339) || query.Get("end_time") != until.Format(time.RFC3339) {
		t.Fatalf("exact range = %s to %s", query.Get("start_time"), query.Get("end_time"))
	}
}

func TestUsageTotalEndpointUsesEpochForAllRange(t *testing.T) {
	endpoint := usageTotalEndpoint("https://relay.example/", time.Time{}, time.Date(2026, 8, 11, 16, 30, 0, 0, time.UTC))
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("start_time") != time.Unix(0, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("all-range start_time = %q", query.Get("start_time"))
	}
	if query.Get("start_date") == "" {
		t.Fatal("all-range start_date is missing")
	}
}

func TestUsageCacheKeyIncludesExactRange(t *testing.T) {
	first := time.Date(2026, 8, 23, 0, 0, 0, 0, time.Local)
	second := first.Add(24 * time.Hour)
	if usageCacheKey("accounts", 1, "today", first) == usageCacheKey("accounts", 1, "today", second) {
		t.Fatal("usage cache key reused different interval starts")
	}
	if usageCacheKey("accounts", 1, "today", time.Time{}) == usageCacheKey("accounts", 1, "today", first) {
		t.Fatal("all-time usage cache key collided with bounded interval")
	}
}

func TestAccountUsageCacheKeyTracksBoundAccounts(t *testing.T) {
	since := time.Date(2026, 8, 23, 0, 0, 0, 0, time.Local)
	first := accountUsageCacheKey(1, "today", since, []int64{498, 496, 498})
	if first != accountUsageCacheKey(1, "today", since, []int64{496, 498}) {
		t.Fatal("account usage cache key changed with account ordering or duplicates")
	}
	if first == accountUsageCacheKey(1, "today", since, []int64{496, 497}) {
		t.Fatal("account usage cache key collided for distinct account bindings")
	}
	if first == accountUsageCacheKey(1, "today", since.Add(24*time.Hour), []int64{496, 498}) {
		t.Fatal("account usage cache key reused a different interval")
	}
	if got := normalizedUsageAccountIDs([]int64{0, 498, 496, 498, -1}); !reflect.DeepEqual(got, []int64{496, 498}) {
		t.Fatalf("unique account IDs = %v", got)
	}
}

func TestUserUsageStatsEndpointIncludesUserFilter(t *testing.T) {
	since := time.Date(2026, 8, 10, 16, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	until := since.Add(24 * time.Hour)
	endpoint := userUsageStatsEndpoint("https://relay.example/", 77, since, until)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/api/v1/admin/usage/stats" || parsed.Query().Get("user_id") != "77" {
		t.Fatalf("endpoint = %s", endpoint)
	}
	if parsed.Query().Get("start_time") != since.Format(time.RFC3339) || parsed.Query().Get("end_time") != until.Format(time.RFC3339) {
		t.Fatalf("exact range = %s", endpoint)
	}
}

func TestUserRankingEndpointUsesDateRangeAndBoundedLimit(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	previousLocal := time.Local
	time.Local = location
	defer func() { time.Local = previousLocal }()
	since := time.Date(2026, 8, 12, 0, 0, 0, 0, location)
	until := time.Date(2026, 8, 18, 12, 0, 0, 0, location)
	endpoint := userRankingEndpoint("https://relay.example/", since, until)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Path != "/api/v1/admin/dashboard/users-ranking" || query.Get("limit") != "50" {
		t.Fatalf("endpoint = %s", endpoint)
	}
	if query.Get("start_date") != "2026-08-12" || query.Get("end_date") != "2026-08-18" {
		t.Fatalf("date range = %s to %s", query.Get("start_date"), query.Get("end_date"))
	}
}

func TestUserRankingCompletenessChecksAggregateTotals(t *testing.T) {
	complete := remoteUserRanking{
		Ranking:         []remoteUserRankingItem{{UserID: 1, Tokens: 100, ActualCost: 1.25}, {UserID: 2, Tokens: 50, ActualCost: 0.75}},
		TotalTokens:     150,
		TotalActualCost: 2,
	}
	if !userRankingComplete(complete) {
		t.Fatal("matching ranking totals should be complete")
	}
	complete.TotalTokens++
	if userRankingComplete(complete) {
		t.Fatal("mismatched ranking totals should be incomplete")
	}
}

func TestUserUsageCandidatesFollowLastUsedRange(t *testing.T) {
	cutoff := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	old := cutoff.Add(-time.Hour)
	recent := cutoff.Add(time.Hour)
	users := []remoteUser{
		{ID: 1, LastUsedAt: &old},
		{ID: 2, LastUsedAt: &recent},
		{ID: 3},
	}
	if got := userUsageCandidates(users, "today", cutoff); !reflect.DeepEqual(got, map[int64]struct{}{2: {}}) {
		t.Fatalf("today candidates = %#v", got)
	}
	if got := userUsageCandidates(users, "all", time.Time{}); !reflect.DeepEqual(got, map[int64]struct{}{1: {}, 2: {}}) {
		t.Fatalf("all candidates = %#v", got)
	}
}

func TestSortUserManagementItemsUsesFullResultOrdering(t *testing.T) {
	last := time.Date(2026, 8, 17, 3, 42, 14, 0, time.UTC)
	items := []UserManagementItem{
		{ID: 1, Balance: 10, Usage: 9, CurrentConcurrency: 2, CreatedAt: last.Add(-2 * time.Hour)},
		{ID: 2, Balance: 30, Usage: 1, CurrentConcurrency: 8, LastUsedAt: &last, CreatedAt: last.Add(-3 * time.Hour)},
		{ID: 3, Balance: 20, Usage: 12, CurrentConcurrency: 4, CreatedAt: last.Add(-time.Hour)},
	}
	sortUserManagementItems(items, "balance", "desc")
	if got := []int64{items[0].ID, items[1].ID, items[2].ID}; !reflect.DeepEqual(got, []int64{2, 3, 1}) {
		t.Fatalf("balance order = %v", got)
	}
	sortUserManagementItems(items, "usage", "asc")
	if got := []int64{items[0].ID, items[1].ID, items[2].ID}; !reflect.DeepEqual(got, []int64{2, 1, 3}) {
		t.Fatalf("usage order = %v", got)
	}
	sortUserManagementItems(items, "last_used_at", "desc")
	if got := []int64{items[0].ID, items[1].ID, items[2].ID}; !reflect.DeepEqual(got, []int64{2, 1, 3}) {
		t.Fatalf("last-used order = %v", got)
	}
}

func TestUpstreamProbeCostUsesResolvedMultiplier(t *testing.T) {
	cost, observed := upstreamProbeCost(map[string]any{
		"upstream_billing_probe": map[string]any{
			"status": "ok",
			"data": map[string]any{
				"resolved_rate_multiplier": 0.37,
				"observed_at":              "2026-08-10T05:00:00Z",
			},
		},
	})
	if cost == nil || *cost != 0.37 {
		t.Fatalf("cost = %#v", cost)
	}
	if observed == nil || !observed.Equal(time.Date(2026, 8, 10, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("observed = %#v", observed)
	}
}

func TestUpstreamProbeCostRejectsMissingOrFailedProbe(t *testing.T) {
	if cost, _ := upstreamProbeCost(map[string]any{"upstream_billing_probe": map[string]any{"status": "failed"}}); cost != nil {
		t.Fatalf("failed probe cost = %v", *cost)
	}
	if cost, _ := upstreamProbeCost(map[string]any{"upstream_billing_probe": map[string]any{"status": "ok", "data": map[string]any{}}}); cost != nil {
		t.Fatalf("missing probe cost = %v", *cost)
	}
}

func TestRetainLastProbeCostPreservesSuccessfulValue(t *testing.T) {
	oldCost := 0.27
	oldObserved := time.Date(2026, 8, 19, 3, 4, 5, 0, time.UTC)
	cost, observed := retainLastProbeCost(nil, nil, storage.RelayAccount{
		RateMultiplier: &oldCost,
		RateSource:     "upstream_probe",
		RateObservedAt: &oldObserved,
	})
	if cost == nil || *cost != oldCost {
		t.Fatalf("cost = %#v", cost)
	}
	if observed == nil || !observed.Equal(oldObserved) {
		t.Fatalf("observed = %#v", observed)
	}
}

func TestRetainLastProbeCostDoesNotInventUnknownCost(t *testing.T) {
	remoteDefault := 1.0
	for _, previous := range []storage.RelayAccount{
		{},
		{RateMultiplier: &remoteDefault},
		{RateMultiplier: &remoteDefault, RateSource: "manual"},
	} {
		if cost, observed := retainLastProbeCost(nil, nil, previous); cost != nil || observed != nil {
			t.Fatalf("unexpected retained result: cost=%#v observed=%#v", cost, observed)
		}
	}
}

func TestRetainLastProbeCostPrefersFreshProbe(t *testing.T) {
	oldCost, newCost := 0.27, 0.31
	oldObserved := time.Date(2026, 8, 19, 3, 4, 5, 0, time.UTC)
	newObserved := oldObserved.Add(time.Hour)
	cost, observed := retainLastProbeCost(&newCost, &newObserved, storage.RelayAccount{
		RateMultiplier: &oldCost, RateSource: "upstream_probe", RateObservedAt: &oldObserved,
	})
	if cost == nil || *cost != newCost || observed == nil || !observed.Equal(newObserved) {
		t.Fatalf("cost=%#v observed=%#v", cost, observed)
	}
}

func TestProbeAPIKeyAccountsUsesTwentyAccountBatches(t *testing.T) {
	var batches [][]int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/accounts/upstream-billing-probe/batch" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "secret" {
			t.Fatalf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		var body struct {
			AccountIDs []int64 `json:"account_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		batches = append(batches, body.AccountIDs)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"results":[]}}`))
	}))
	defer server.Close()

	accounts := make([]remoteAccount, 0, 46)
	for id := int64(1); id <= 45; id++ {
		accounts = append(accounts, remoteAccount{ID: id, Type: "apikey"})
	}
	accounts = append(accounts, remoteAccount{ID: 999, Type: "oauth"})
	svc := &Service{client: server.Client()}
	if err := svc.probeAPIKeyAccounts(context.Background(), server.URL, "secret", accounts); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 {
		t.Fatalf("batch count = %d", len(batches))
	}
	if got := fmt.Sprint([]int{len(batches[0]), len(batches[1]), len(batches[2])}); got != "[20 20 5]" {
		t.Fatalf("batch sizes = %s", got)
	}
	for _, batch := range batches {
		for _, id := range batch {
			if id == 999 {
				t.Fatal("oauth account was included in billing probe")
			}
		}
	}
}

func TestDeleteRemoteUsesAdminKeyAndAcceptsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/accounts/1888" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "secret" {
			t.Fatalf("x-api-key = %q", r.Header.Get("x-api-key"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	if err := svc.deleteRemote(context.Background(), server.URL+"/api/v1/admin/accounts/1888", "secret"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRemoteAcceptsEnvelopeAndReturnsRemoteReason(t *testing.T) {
	responses := []struct {
		status int
		body   string
	}{
		{status: http.StatusOK, body: `{"code":0,"data":null}`},
		{status: http.StatusConflict, body: `{"code":409,"message":"分组仍有关联账号"}`},
	}
	index := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := responses[index]
		index++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = w.Write([]byte(response.body))
	}))
	defer server.Close()

	svc := &Service{client: server.Client()}
	if err := svc.deleteRemote(context.Background(), server.URL+"/api/v1/admin/groups/42", "secret"); err != nil {
		t.Fatal(err)
	}
	err := svc.deleteRemote(context.Background(), server.URL+"/api/v1/admin/groups/42", "secret")
	if err == nil || !strings.Contains(err.Error(), "分组仍有关联账号") {
		t.Fatalf("error = %v", err)
	}
}

func TestAutomaticPriorityTargets(t *testing.T) {
	samples := func(first, duration int64) string {
		body, err := json.Marshal([]storage.RelayLatencySample{{FirstTokenMS: first, DurationMS: duration}})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	accounts := []storage.RelayAccount{
		{ExternalID: 1, Type: "oauth"},
		{ExternalID: 2, Type: "apikey"},
		{ExternalID: 3, Type: "apikey", LatencySamplesJSON: samples(1_000, 10_000)},
		{ExternalID: 4, Type: "apikey", LatencySamplesJSON: samples(5_000, 30_000)},
		{ExternalID: 5, Type: "apikey", LatencySamplesJSON: samples(10_000, 60_000)},
		{ExternalID: 6, Type: "apikey", LatencySamplesJSON: samples(5_000, 30_000)},
		{ExternalID: 7, Type: "other"},
	}
	targets := automaticPriorityTargets(accounts)
	if targets[1] != 1 || targets[2] != 2 || targets[3] != 2 || targets[4] != 6 || targets[5] != 10 || targets[6] != 6 {
		t.Fatalf("targets = %#v", targets)
	}
	if _, ok := targets[7]; ok {
		t.Fatalf("unsupported account type received priority: %#v", targets)
	}
}

func TestAutomaticPriorityRecallRaisesIdleAccountsOneLevel(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-30 * time.Minute)
	idle := now.Add(-4 * time.Hour)
	samples := func(first, duration int64) string {
		body, err := json.Marshal([]storage.RelayLatencySample{{FirstTokenMS: first, DurationMS: duration}})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	accounts := []storage.RelayAccount{
		{ExternalID: 1, Type: "oauth", Priority: 1, Schedulable: true},
		{ExternalID: 2, Type: "apikey", Priority: 8, Schedulable: true, LastUsedAt: &idle},
		{ExternalID: 3, Type: "apikey", Priority: 7, Schedulable: true, LastUsedAt: &recent},
		{ExternalID: 4, Type: "apikey", Priority: 2, Schedulable: true},
		{ExternalID: 5, Type: "apikey", Priority: 10, Schedulable: true, LastUsedAt: &idle, LatencySamplesJSON: samples(10_000, 60_000)},
		{ExternalID: 6, Type: "apikey", Priority: 2, Schedulable: true, LastUsedAt: &recent, LatencySamplesJSON: samples(1_000, 10_000)},
	}
	targets := automaticPriorityTargetsWithRecall(accounts, true, 180, now)
	if targets[1] != 1 || targets[2] != 7 || targets[3] != 2 || targets[4] != 2 || targets[5] != 9 || targets[6] != 2 {
		t.Fatalf("recall targets = %#v", targets)
	}
}

func TestRuntimeSettingsAdjustmentLogCapturesAutomaticPriorityChange(t *testing.T) {
	oldPriority, targetPriority := 7, 3
	account := storage.RelayAccount{
		ExternalID: 42, Name: "priority-account", Platform: "openai",
		Concurrency: 20, Priority: oldPriority,
	}
	log := runtimeSettingsAdjustmentLog(account, 9, &targetPriority, "auto")
	if log.RelayStationID != 9 || log.RelayAccountExternalID != account.ExternalID {
		t.Fatalf("log identity = %#v", log)
	}
	if log.Source != "auto" || log.Action != "priority_update" {
		t.Fatalf("source/action = %q/%q", log.Source, log.Action)
	}
	if log.OldPriority == nil || *log.OldPriority != oldPriority || log.NewPriority == nil || *log.NewPriority != targetPriority {
		t.Fatalf("priority change = %#v -> %#v", log.OldPriority, log.NewPriority)
	}
	if log.OldConcurrency != nil || log.NewConcurrency != nil {
		t.Fatalf("unexpected concurrency change = %#v -> %#v", log.OldConcurrency, log.NewConcurrency)
	}
	if log.OldGroupIDsJSON != "[]" || log.NewGroupIDsJSON != "[]" {
		t.Fatalf("runtime log group placeholders = %q -> %q", log.OldGroupIDsJSON, log.NewGroupIDsJSON)
	}
}

func TestRuntimeSettingsAdjustmentLogKeepsOnlyPriorityChange(t *testing.T) {
	priority := 8
	account := storage.RelayAccount{ExternalID: 7, Concurrency: 10, Priority: 4, PoolModeRetryCount: 3}
	log := runtimeSettingsAdjustmentLog(account, 2, &priority, "manual")
	if log.Source != "manual" || log.Action != "priority_update" {
		t.Fatalf("source/action = %q/%q", log.Source, log.Action)
	}
	if log.OldConcurrency != nil || log.NewConcurrency != nil || log.OldPoolModeRetryCount != nil || log.NewPoolModeRetryCount != nil {
		t.Fatalf("manual-only fields leaked into log: %#v", log)
	}
	if log.OldPriority == nil || *log.OldPriority != 4 || log.NewPriority == nil || *log.NewPriority != priority {
		t.Fatalf("priority change = %#v -> %#v", log.OldPriority, log.NewPriority)
	}
	if !shouldRecordRuntimeSettingsAdjustment(log) {
		t.Fatal("priority change was not marked for recording")
	}
}

func TestRuntimeSettingsAdjustmentLogSkipsManualOnlyChange(t *testing.T) {
	account := storage.RelayAccount{ExternalID: 7, Concurrency: 10, Priority: 4, PoolModeRetryCount: 3}
	log := runtimeSettingsAdjustmentLog(account, 2, nil, "manual")
	if shouldRecordRuntimeSettingsAdjustment(log) {
		t.Fatalf("manual-only change was marked for recording: %#v", log)
	}
}

func TestChannelPricingCanonicalizesOrder(t *testing.T) {
	inputPrice := 1.25
	outputPrice := 2.5
	channelA := remoteChannel{
		BillingModelSource:         "channel",
		ApplyPricingToAccountStats: true,
		ModelPricing: []remoteModelPricing{
			{ID: 2, Models: []string{"gpt", "claude"}, OutputPrice: &outputPrice},
			{ID: 1, Models: []string{"gpt"}, InputPrice: &inputPrice},
		},
		AccountStatsPricingRules: []remotePricingRule{
			{ID: 20, GroupIDs: []int64{2, 1}, AccountIDs: []int64{9, 3}, Pricing: []remoteModelPricing{{ID: 4, Models: []string{"gpt", "claude"}}}},
			{ID: 10, GroupIDs: []int64{4, 3}, Pricing: []remoteModelPricing{{ID: 3, Models: []string{"claude"}}}},
		},
	}
	channelB := remoteChannel{
		BillingModelSource:         "channel",
		ApplyPricingToAccountStats: true,
		ModelPricing: []remoteModelPricing{
			{ID: 1, Models: []string{"gpt"}, InputPrice: &inputPrice},
			{ID: 2, Models: []string{"claude", "gpt"}, OutputPrice: &outputPrice},
		},
		AccountStatsPricingRules: []remotePricingRule{
			{ID: 10, GroupIDs: []int64{3, 4}, Pricing: []remoteModelPricing{{ID: 3, Models: []string{"claude"}}}},
			{ID: 20, GroupIDs: []int64{1, 2}, AccountIDs: []int64{3, 9}, Pricing: []remoteModelPricing{{ID: 4, Models: []string{"claude", "gpt"}}}},
		},
	}

	bodyA, hashA, modelsA, modelCountA, ruleCountA, err := channelPricing(channelA)
	if err != nil {
		t.Fatal(err)
	}
	bodyB, hashB, modelsB, modelCountB, ruleCountB, err := channelPricing(channelB)
	if err != nil {
		t.Fatal(err)
	}
	if bodyA != bodyB || hashA != hashB {
		t.Fatalf("equivalent pricing produced different snapshots\nA: %s\nB: %s", bodyA, bodyB)
	}
	if modelsA != `["claude","gpt"]` || modelsB != modelsA {
		t.Fatalf("models = %s and %s", modelsA, modelsB)
	}
	if modelCountA != 2 || modelCountB != 2 || ruleCountA != 2 || ruleCountB != 2 {
		t.Fatalf("unexpected counts: models %d/%d rules %d/%d", modelCountA, modelCountB, ruleCountA, ruleCountB)
	}

	changedPrice := 1.5
	channelB.ModelPricing[0].InputPrice = &changedPrice
	_, changedHash, _, _, _, err := channelPricing(channelB)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == hashA {
		t.Fatal("price change did not change the pricing hash")
	}
}

func TestChannelSiteMatchesAccountBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		site    string
		account string
		want    bool
	}{
		{name: "same root", site: "https://example.com", account: "https://example.com/", want: true},
		{name: "account v1 suffix", site: "https://example.com", account: "https://example.com/v1", want: true},
		{name: "site api suffix", site: "https://example.com/api", account: "https://example.com", want: true},
		{name: "nested path boundary", site: "https://example.com/api", account: "https://example.com/api/v1", want: true},
		{name: "default port", site: "https://example.com:443", account: "https://example.com/v1", want: true},
		{name: "different registrable domain", site: "https://example.com", account: "https://api.example.net/v1", want: false},
		{name: "provider subdomain", site: "https://walkcoding.top", account: "https://st.walkcoding.top", want: true},
		{name: "subdomain site", site: "https://st.walkcoding.top", account: "https://walkcoding.top", want: true},
		{name: "provider api domain", site: "https://api.mhapi.cn", account: "https://mhapi.net", want: true},
		{name: "lookalike host", site: "https://example.com", account: "https://example.com.evil.test/v1", want: false},
		{name: "lookalike subdomain", site: "https://walkcoding.top", account: "https://walkcoding.top.evil.test/v1", want: false},
		{name: "path without boundary", site: "https://example.com/api", account: "https://example.com/apiv2", want: false},
		{name: "different paths", site: "https://example.com/api", account: "https://example.com/v1", want: false},
		{name: "invalid", site: "", account: "https://example.com", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := channelSiteMatchesAccountBaseURL(test.site, test.account); got != test.want {
				t.Fatalf("match(%q, %q) = %v, want %v", test.site, test.account, got, test.want)
			}
		})
	}
}

func TestResolveChannelUsageBindingsPrefersExplicitBinding(t *testing.T) {
	channelOneID, channelTwoID, missingChannelID := uint(1), uint(2), uint(99)
	channels := []storage.Channel{
		{ID: channelOneID, SiteURL: "https://one.example"},
		{ID: channelTwoID, SiteURL: "https://two.example/api"},
	}
	accounts := []storage.RelayAccount{
		{ExternalID: 11, BaseURL: "https://one.example/v1"},
		{ExternalID: 12, BaseURL: "https://one.example/v1"},
		{ExternalID: 13, BaseURL: "https://two.example/api/v1"},
		{ExternalID: 14, BaseURL: "https://one.example"},
		{ExternalID: 15, BaseURL: "https://unknown.example"},
	}
	overrides := []storage.RelayAccountCostOverride{
		{RelayAccountExternalID: 11, Mode: "channel_group", MonitorChannelID: &channelTwoID, UpstreamGroup: "explicit"},
		{RelayAccountExternalID: 12, Mode: "manual"},
		{RelayAccountExternalID: 14, Mode: "channel_group", MonitorChannelID: &missingChannelID, UpstreamGroup: "stale"},
	}

	got := resolveChannelUsageBindings(channels, accounts, overrides)
	want := map[int64]uint{11: channelTwoID, 12: channelOneID, 13: channelTwoID}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("channel usage bindings = %#v, want %#v", got, want)
	}
}

func TestResolveChannelUsageBindingsSkipsAmbiguousBaseURL(t *testing.T) {
	channels := []storage.Channel{
		{ID: 1, SiteURL: "https://shared.example"},
		{ID: 2, SiteURL: "https://shared.example/api"},
	}
	accounts := []storage.RelayAccount{{ExternalID: 11, BaseURL: "https://shared.example/api/v1"}}
	if got := resolveChannelUsageBindings(channels, accounts, nil); len(got) != 0 {
		t.Fatalf("ambiguous base URL was attributed: %#v", got)
	}
}

func TestResolveChannelUsageBindingsRequiresExplicitBindingForManualChannel(t *testing.T) {
	channels := []storage.Channel{{ID: 1, BalanceMode: storage.BalanceModeManual, SiteURL: "https://manual.example"}}
	accounts := []storage.RelayAccount{{ExternalID: 11, BaseURL: "https://manual.example/v1"}}
	if got := resolveChannelUsageBindings(channels, accounts, nil); len(got) != 0 {
		t.Fatalf("manual channel was attributed without binding: %#v", got)
	}

	channelID := uint(1)
	got := resolveChannelUsageBindings(channels, accounts, []storage.RelayAccountCostOverride{{
		RelayAccountExternalID: 11, Mode: "auto_link", MonitorChannelID: &channelID, UpstreamGroup: "账号 A",
	}})
	if !reflect.DeepEqual(got, map[int64]uint{11: 1}) {
		t.Fatalf("auto link was not attributed: %#v", got)
	}
}

func TestAutomaticManualChannelBindingsOnlyUsesKnownUniqueUnboundAccounts(t *testing.T) {
	known := 0.72
	channels := []storage.Channel{
		{ID: 1, BalanceMode: storage.BalanceModeManual, SiteURL: "https://one.example"},
		{ID: 2, BalanceMode: storage.BalanceModeManual, SiteURL: "https://shared.example"},
		{ID: 3, BalanceMode: storage.BalanceModeManual, SiteURL: "https://shared.example/api"},
	}
	accounts := []storage.RelayAccount{
		{ExternalID: 11, Name: "可自动关联", BaseURL: "https://one.example/v1", RateMultiplier: &known, RateSource: "upstream_probe"},
		{ExternalID: 12, Name: "没有倍率", BaseURL: "https://one.example/v1"},
		{ExternalID: 13, Name: "手动覆盖", BaseURL: "https://one.example/v1", RateMultiplier: &known, RateSource: "upstream_probe"},
		{ExternalID: 14, Name: "地址歧义", BaseURL: "https://shared.example/api/v1", RateMultiplier: &known, RateSource: "upstream_probe"},
	}
	bindings := automaticManualChannelBindings(channels, storage.RelayStation{ID: 7, Name: "中转站 A"}, accounts, []storage.RelayAccountCostOverride{{
		RelayAccountExternalID: 13, Mode: "channel_group",
	}})
	want := []storage.AutomaticChannelAccountBinding{{
		ChannelID: 1, RelayStationID: 7, RelayStationName: "中转站 A",
		RelayAccountExternalID: 11, RelayAccountName: "可自动关联", RateMultiplier: known,
	}}
	if !reflect.DeepEqual(bindings, want) {
		t.Fatalf("automatic bindings = %#v, want %#v", bindings, want)
	}
}

func TestResolveChannelUsageCostBindingsUsesSelectedGroupMultiplier(t *testing.T) {
	channelID := uint(1)
	channels := []storage.Channel{{ID: channelID, BalanceMode: storage.BalanceModeManual, SiteURL: "https://manual.example"}}
	accounts := []storage.RelayAccount{{ExternalID: 11, BaseURL: "https://unrelated.example"}}
	overrides := []storage.RelayAccountCostOverride{{
		RelayAccountExternalID: 11, Mode: "channel_group", MonitorChannelID: &channelID, UpstreamGroup: "自定义组",
	}}
	rates := map[uint][]storage.RateSnapshot{
		channelID: {{ChannelID: channelID, ModelName: "自定义组", Ratio: 0.37}},
	}
	bindings := resolveChannelUsageCostBindings(channels, accounts, overrides, rates)
	binding, ok := bindings[11]
	if !ok || binding.ChannelID != channelID || binding.GroupName != "自定义组" || binding.Multiplier == nil || *binding.Multiplier != 0.37 {
		t.Fatalf("cost binding = %#v, want channel %d with multiplier 0.37", binding, channelID)
	}
}

func TestChannelBoundAccountCostFallsBackToBaseCost(t *testing.T) {
	multiplier := 0.08
	binding := channelUsageCostBinding{Mode: "channel_group", Multiplier: &multiplier}
	got := channelBoundAccountCost(AccountUsageStats{BaseCost: 12.5, AccountCost: 0}, binding)
	if got != 1.0 {
		t.Fatalf("fallback account cost = %v, want 1", got)
	}

	autoBinding := channelUsageCostBinding{Mode: "auto_link", Multiplier: &multiplier}
	got = channelBoundAccountCost(AccountUsageStats{BaseCost: 12.5, AccountCost: 2.25}, autoBinding)
	if got != 1.0 {
		t.Fatalf("bound account cost = %v, want 1", got)
	}
	got = channelBoundAccountCost(AccountUsageStats{AccountCost: 2.25}, channelUsageCostBinding{})
	if got != 2.25 {
		t.Fatalf("unbound account cost = %v, want 2.25", got)
	}
}

func TestChannelUsageCostBasisChangesWithMultiplierOrAccountSet(t *testing.T) {
	base := []channelUsageCostBasisEntry{
		{RelayStationID: 1, RelayAccountExternalID: 547, MultiplierBits: math.Float64bits(0.04)},
		{RelayStationID: 1, RelayAccountExternalID: 1895, MultiplierBits: math.Float64bits(0.55)},
	}
	reordered := []channelUsageCostBasisEntry{base[1], base[0]}
	if channelUsageCostBasis(base) != channelUsageCostBasis(reordered) {
		t.Fatal("cost basis must be independent of account iteration order")
	}

	changedMultiplier := append([]channelUsageCostBasisEntry(nil), base...)
	changedMultiplier[0].MultiplierBits = math.Float64bits(0.10)
	if channelUsageCostBasis(base) == channelUsageCostBasis(changedMultiplier) {
		t.Fatal("cost basis did not change with the effective multiplier")
	}

	changedAccounts := append(append([]channelUsageCostBasisEntry(nil), base...), channelUsageCostBasisEntry{
		RelayStationID: 1, RelayAccountExternalID: 1904, MultiplierBits: math.Float64bits(0.05),
	})
	if channelUsageCostBasis(base) == channelUsageCostBasis(changedAccounts) {
		t.Fatal("cost basis did not change with the bound account set")
	}
}

func TestNormalizeUsageIPAddress(t *testing.T) {
	tests := []struct {
		name         string
		value        string
		wantAddress  string
		wantLocation string
	}{
		{name: "public IPv4", value: " 23.148.228.88 ", wantAddress: "23.148.228.88"},
		{name: "forwarded chain", value: "23.148.228.88, 10.0.0.2", wantAddress: "23.148.228.88"},
		{name: "private IPv4 with port", value: "192.168.1.8:8080", wantAddress: "192.168.1.8", wantLocation: "内网地址"},
		{name: "IPv6 with port", value: "[::1]:443", wantAddress: "::1", wantLocation: "本机地址"},
		{name: "invalid", value: "not-an-ip", wantAddress: "not-an-ip", wantLocation: "无效地址"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			address, location := normalizeUsageIPAddress(test.value)
			if address != test.wantAddress || location != test.wantLocation {
				t.Fatalf("normalizeUsageIPAddress(%q) = (%q, %q), want (%q, %q)", test.value, address, location, test.wantAddress, test.wantLocation)
			}
		})
	}
}

func TestFormatIPLocation(t *testing.T) {
	if got := formatIPLocation("US", "California", "Los Angeles"); got != "US · California · Los Angeles" {
		t.Fatalf("location = %q", got)
	}
	if got := formatIPLocation("SG", "Singapore", "Singapore"); got != "SG · Singapore" {
		t.Fatalf("deduplicated location = %q", got)
	}
}

func TestUserRiskScoreFlagsSharedRegistrationIP(t *testing.T) {
	created := time.Now().Add(-48 * time.Hour)
	user := remoteUser{Email: "person@example.com", CreatedAt: created}
	score, level, reasons := userRiskScore(user, registrationIPInfo{IP: "203.0.113.10", Count: 5, BurstCount: 3}, UserUsageStats{})
	if score != 70 || level != "medium" {
		t.Fatalf("risk = %d/%s, want 70/medium", score, level)
	}
	if len(reasons) != 2 {
		t.Fatalf("reasons = %#v, want shared IP and burst", reasons)
	}
}

func TestSuspiciousEmailMatchesRegistrationPattern(t *testing.T) {
	if !suspiciousEmail("alice.smith12345@gmail.com") {
		t.Fatal("expected machine-like gmail to be suspicious")
	}
	if suspiciousEmail("alice@example.com") || suspiciousEmail("alice@gmail.com") {
		t.Fatal("ordinary emails must not be suspicious")
	}
}
