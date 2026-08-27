package relay

import (
	"reflect"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestRetryOnlyRuntimeSettingsIsNotNoop(t *testing.T) {
	if !hasRuntimeSettingsChanges(map[string]any{}, true) {
		t.Fatal("retry-only runtime update was treated as a no-op")
	}
	if hasRuntimeSettingsChanges(map[string]any{}, false) {
		t.Fatal("empty runtime update was treated as a change")
	}
}

func TestMergePoolModeRetryCredentialsPreservesSecrets(t *testing.T) {
	raw := map[string]any{"api_key": "secret", "base_url": "https://example.com", "pool_mode": false, "pool_mode_retry_count": 3}
	got := mergePoolModeRetryCredentials(raw, 7)
	if got["api_key"] != "secret" || got["base_url"] != "https://example.com" {
		t.Fatalf("existing credentials were not preserved: %#v", got)
	}
	if got["pool_mode"] != true || got["pool_mode_retry_count"] != 7 {
		t.Fatalf("pool retry settings = %#v", got)
	}
	if raw["pool_mode"] != false || raw["pool_mode_retry_count"] != 3 {
		t.Fatalf("source credentials were mutated: %#v", raw)
	}
}

func TestAppendGroupIDPreservesEveryExistingAssignment(t *testing.T) {
	current := []int64{6, 10, 6}
	if got, want := appendGroupID(current, 28), []int64{6, 10, 28}; !reflect.DeepEqual(got, want) {
		t.Fatalf("appendGroupID() = %v, want %v", got, want)
	}
	if got, want := appendGroupID(current, 10), []int64{6, 10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("append existing group = %v, want %v", got, want)
	}
	if got, want := appendGroupIDs(current, []int64{28, 10, 32, 28}), []int64{6, 10, 28, 32}; !reflect.DeepEqual(got, want) {
		t.Fatalf("append multiple groups = %v, want %v", got, want)
	}
}

func TestEvaluateAccountRiskReplacesOnlyUnsafeGroups(t *testing.T) {
	cost := 1.25
	account := storage.RelayAccount{ExternalID: 8, Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ExternalID: 1, Name: "亏损", Platform: "openai", Status: "active", RateMultiplier: 1.2},
		{ExternalID: 2, Name: "安全", Platform: "openai", Status: "active", RateMultiplier: 1.5},
		{ExternalID: 3, Name: "更低安全候选", Platform: "openai", Status: "active", RateMultiplier: 1.3},
		{ExternalID: 4, Name: "异平台", Platform: "anthropic", Status: "active", RateMultiplier: 1.21},
	}
	risk := evaluateAccountRisk(account, groups[:2], groups, 0.05)
	if risk.State != RiskStateRisk || !risk.CanApply {
		t.Fatalf("state=%q canApply=%v", risk.State, risk.CanApply)
	}
	if len(risk.UnsafeGroups) != 1 || risk.UnsafeGroups[0].ExternalID != 1 {
		t.Fatalf("unsafe groups = %#v", risk.UnsafeGroups)
	}
	if risk.RecommendedGroup == nil || risk.RecommendedGroup.ExternalID != 3 {
		t.Fatalf("recommended = %#v", risk.RecommendedGroup)
	}
	// 已有关联的安全分组保留，不能为了找最低候选把它从账号上移除。
	if len(risk.SuggestedGroupIDs) != 1 || risk.SuggestedGroupIDs[0] != 2 {
		t.Fatalf("suggested ids = %v", risk.SuggestedGroupIDs)
	}
}

func TestEvaluateAccountRiskEqualMultiplierIsNoProfit(t *testing.T) {
	cost := 1.2
	account := storage.RelayAccount{ExternalID: 8, Type: "apikey", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Name: "同倍率", Platform: "openai", Status: "active", RateMultiplier: 1.2},
		{ID: 2, ExternalID: 2, Name: "有利润", Platform: "openai", Status: "active", RateMultiplier: 1.4},
	}
	risk := evaluateAccountRisk(account, groups[:1], groups, 0)
	if risk.State != RiskStateNoProfit || !risk.CanApply {
		t.Fatalf("state=%q canApply=%v", risk.State, risk.CanApply)
	}
	if len(risk.NoProfitGroups) != 1 || risk.NoProfitGroups[0].ExternalID != 1 {
		t.Fatalf("no-profit groups = %#v", risk.NoProfitGroups)
	}
	if risk.RecommendedGroup == nil || risk.RecommendedGroup.ExternalID != 2 {
		t.Fatalf("recommended = %#v", risk.RecommendedGroup)
	}
	if len(risk.SuggestedGroupIDs) != 1 || risk.SuggestedGroupIDs[0] != 2 {
		t.Fatalf("suggested ids = %v", risk.SuggestedGroupIDs)
	}
}

func TestEvaluateAccountRiskNoProfitWithoutProfitableCandidateStaysNoProfit(t *testing.T) {
	cost := 1.2
	account := storage.RelayAccount{ExternalID: 8, Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Name: "同倍率", Platform: "openai", Status: "active", RateMultiplier: 1.2}}
	risk := evaluateAccountRisk(account, groups, groups, 0)
	if risk.State != RiskStateNoProfit || risk.CanApply || risk.RecommendedGroup != nil {
		t.Fatalf("state=%q canApply=%v recommended=%#v", risk.State, risk.CanApply, risk.RecommendedGroup)
	}
}

func TestEvaluateAccountRiskSkipsOAuthOnlyCandidateForAPIKey(t *testing.T) {
	cost := 1.0
	account := storage.RelayAccount{ExternalID: 8, Type: "apikey", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Name: "亏损", Platform: "openai", Status: "active", RateMultiplier: 0.8},
		{ID: 2, ExternalID: 2, Name: "OAuth 候选", Platform: "openai", Status: "active", RequireOAuthOnly: true, RateMultiplier: 1.1},
		{ID: 3, ExternalID: 3, Name: "API Key 候选", Platform: "openai", Status: "active", RateMultiplier: 1.2},
	}
	risk := evaluateAccountRisk(account, groups[:1], groups, 0)
	if risk.RecommendedGroup == nil || risk.RecommendedGroup.ExternalID != 3 {
		t.Fatalf("recommended = %#v", risk.RecommendedGroup)
	}
}

func TestEvaluateAccountRiskAddsLowestSafeCandidate(t *testing.T) {
	cost := 1.0
	account := storage.RelayAccount{ExternalID: 8, Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ExternalID: 1, Name: "亏损", Platform: "openai", Status: "active", RateMultiplier: 0.9},
		{ExternalID: 2, Name: "高价", Platform: "openai", Status: "active", RateMultiplier: 1.6},
		{ExternalID: 3, Name: "最低安全", Platform: "openai", Status: "active", RateMultiplier: 1.1},
	}
	risk := evaluateAccountRisk(account, groups[:1], groups, 0.05)
	if risk.State != RiskStateRisk || !risk.CanApply {
		t.Fatalf("state=%q canApply=%v", risk.State, risk.CanApply)
	}
	if risk.RecommendedGroup == nil || risk.RecommendedGroup.ExternalID != 3 {
		t.Fatalf("recommended = %#v", risk.RecommendedGroup)
	}
	if len(risk.SuggestedGroupIDs) != 1 || risk.SuggestedGroupIDs[0] != 3 {
		t.Fatalf("suggested ids = %v", risk.SuggestedGroupIDs)
	}
}

func TestEvaluateAccountRiskRestrictsCandidatesByConfiguredModelType(t *testing.T) {
	cost := 1.0
	account := storage.RelayAccount{ExternalID: 8, ModelType: "gpt", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.9},
		{ID: 2, ExternalID: 2, Platform: "openai", Status: "active", ModelTypesJSON: `["claude"]`, RateMultiplier: 1.1},
		{ID: 3, ExternalID: 3, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.2},
	}
	risk := evaluateAccountRiskWithOptions(account, groups[:1], groups, nil, true)
	if risk.RecommendedGroup == nil || risk.RecommendedGroup.ExternalID != 3 {
		t.Fatalf("recommended = %#v", risk.RecommendedGroup)
	}
}

func TestEvaluateAccountRiskRestrictsDowngradeByModelType(t *testing.T) {
	cost := 0.5
	account := storage.RelayAccount{ExternalID: 8, ModelType: "gpt", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.4},
		{ID: 2, ExternalID: 2, Platform: "openai", Status: "active", ModelTypesJSON: `["claude"]`, RateMultiplier: 0.7},
		{ID: 3, ExternalID: 3, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.9},
	}
	risk := evaluateAccountRisk(account, groups[:1], groups, 0)
	if risk.DowngradeGroup == nil || risk.DowngradeGroup.ExternalID != 3 {
		t.Fatalf("downgrade = %#v", risk.DowngradeGroup)
	}
	if got, want := groupIDs(risk.DowngradeGroups), []int64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("downgrade groups = %v, want %v", got, want)
	}

	account.ModelType = ""
	risk = evaluateAccountRisk(account, groups[:1], groups, 0)
	if risk.DowngradeGroup != nil || len(risk.DowngradeGroups) != 0 {
		t.Fatalf("unbound model type must not show downgrade: %#v / %#v", risk.DowngradeGroup, risk.DowngradeGroups)
	}
}

func TestEvaluateAccountRiskKeepsUnconfiguredGroupsCompatible(t *testing.T) {
	if !sameModelType(storage.RelayAccount{}, storage.RelayGroup{}) {
		t.Fatal("unconfigured groups must keep legacy behavior")
	}
}

func TestEvaluateAccountRiskKeepsCurrentSalesGroupWhenModelTypeIsUnbound(t *testing.T) {
	cost := 1.0
	account := storage.RelayAccount{ExternalID: 8, Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Name: "销售组", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.2}}
	risk := evaluateAccountRisk(account, groups, groups, 0)
	if risk.State != RiskStateProtected {
		t.Fatalf("state = %q, current groups = %#v", risk.State, risk.CurrentGroups)
	}
	if len(risk.CurrentGroups) != 1 || risk.CurrentGroups[0].ExternalID != 1 {
		t.Fatalf("current groups = %#v", risk.CurrentGroups)
	}
}

func TestEvaluateAccountRiskNeverAutoChangesUnknownOrNoCandidate(t *testing.T) {
	groups := []storage.RelayGroup{{ExternalID: 1, Name: "亏损", Platform: "openai", Status: "active", RateMultiplier: 1}}
	unknown := evaluateAccountRisk(storage.RelayAccount{Status: "active", Platform: "openai"}, groups, groups, 0)
	if unknown.State != RiskStateCostUnknown || unknown.CanApply {
		t.Fatalf("unknown = %#v", unknown)
	}
	cost := 2.0
	noCandidate := evaluateAccountRisk(storage.RelayAccount{Status: "active", Platform: "openai", RateMultiplier: &cost, RateSource: "upstream_probe"}, groups, groups, 0)
	if noCandidate.State != RiskStateNoSafeCandidate || noCandidate.CanApply {
		t.Fatalf("no candidate = %#v", noCandidate)
	}
}

func TestEvaluateAccountRiskFindsEverySafeDowngradeGroup(t *testing.T) {
	cost := 1.0
	account := storage.RelayAccount{ExternalID: 8, Type: "apikey", ModelType: "gpt", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Name: "当前分组", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.5},
		{ID: 2, ExternalID: 2, Name: "最低安全", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.1},
		{ID: 3, ExternalID: 3, Name: "较高安全", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.2},
		{ID: 4, ExternalID: 4, Name: "无利润", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.0},
		{ID: 5, ExternalID: 5, Name: "异平台", Platform: "anthropic", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.05},
		{ID: 6, ExternalID: 6, Name: "已停用", Platform: "openai", Status: "inactive", ModelTypesJSON: `["gpt"]`, RateMultiplier: 1.01},
		{ID: 7, ExternalID: 7, Name: "OAuth 专属", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RequireOAuthOnly: true, RateMultiplier: 1.02},
	}
	risk := evaluateAccountRisk(account, groups[:1], groups, 0)
	if risk.State != RiskStateProtected {
		t.Fatalf("state = %q", risk.State)
	}
	if risk.DowngradeGroup == nil || risk.DowngradeGroup.ExternalID != 2 {
		t.Fatalf("downgrade group = %#v", risk.DowngradeGroup)
	}
	if got, want := groupIDs(risk.DowngradeGroups), []int64{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("downgrade groups = %v, want %v", got, want)
	}
}

func TestEvaluateAccountRiskHasNoDowngradeAtLowestSafeRate(t *testing.T) {
	cost := 1.0
	account := storage.RelayAccount{ExternalID: 8, Type: "apikey", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Name: "当前最低安全", Platform: "openai", Status: "active", RateMultiplier: 1.1},
		{ID: 2, ExternalID: 2, Name: "更高倍率", Platform: "openai", Status: "active", RateMultiplier: 1.2},
		{ID: 3, ExternalID: 3, Name: "无利润", Platform: "openai", Status: "active", RateMultiplier: 1.0},
	}
	risk := evaluateAccountRisk(account, groups[:1], groups, 0)
	if risk.DowngradeGroup != nil || len(risk.DowngradeGroups) != 0 {
		t.Fatalf("downgrade groups = %#v / %#v", risk.DowngradeGroup, risk.DowngradeGroups)
	}
}
