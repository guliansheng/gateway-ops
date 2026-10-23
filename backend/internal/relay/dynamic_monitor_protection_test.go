package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func dynamicMonitorEnabled(value bool) *bool {
	return &value
}

func TestDynamicMonitorProtectedAccountsNormalizeAndRoundTrip(t *testing.T) {
	source := []int64{30, 10, 30, 0, -1, 10}
	config := DynamicMonitorConfig{
		ProtectedAccountExternalIDs: source,
		Groups:                      []DynamicMonitorRuleGroup{ruleGroup(1, 2, 10)},
	}.Normalize()
	if !slices.Equal(config.ProtectedAccountExternalIDs, []int64{10, 30}) {
		t.Fatalf("protected IDs = %v", config.ProtectedAccountExternalIDs)
	}
	if !slices.Equal(source, []int64{30, 10, 30, 0, -1, 10}) {
		t.Fatalf("normalization mutated input: %v", source)
	}
	if !slices.Equal(config.Groups[0].AccountExternalIDs, []int64{10}) {
		t.Fatal("protection must not remove rule memberships or group assignments")
	}
	body, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	restored := ParseDynamicMonitorConfig(string(body))
	if !slices.Equal(restored.ProtectedAccountExternalIDs, config.ProtectedAccountExternalIDs) {
		t.Fatalf("protection lost after save/parse: %s", body)
	}
	for _, raw := range []string{"", "{}", `{"protected_account_external_ids":null}`, `{"protected_account_external_ids":[0,-1]}`} {
		if got := ParseDynamicMonitorConfig(raw); len(got.ProtectedAccountExternalIDs) != 0 {
			t.Fatalf("legacy/empty protection must be empty: %+v", got)
		}
	}
	legacy := ParseDynamicMonitorConfig(`{"enabled":true,"account_external_ids":[10],"first_token_threshold_ms":1000,"protected_account_external_ids":[10,10,0]}`)
	if !slices.Equal(legacy.ProtectedAccountExternalIDs, []int64{10}) || len(legacy.Groups) != 1 {
		t.Fatalf("legacy migration lost protection or rules: %+v", legacy)
	}
}

func TestDynamicMonitorProtectedAccountsLimitRejectsInsteadOfTruncating(t *testing.T) {
	ids := make([]int64, dynamicMonitorMaxAccounts+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	config := DynamicMonitorConfig{ProtectedAccountExternalIDs: ids}.Normalize()
	if len(config.ProtectedAccountExternalIDs) != len(ids) || config.Validate() == nil {
		t.Fatal("oversized protection must be rejected, never silently truncated")
	}
	if _, err := NewService(nil, nil).SaveDynamicMonitorConfig(1, config); err == nil || !strings.Contains(err.Error(), "1000") {
		t.Fatalf("save must reject oversized protection before accessing storage: %v", err)
	}
	config.ProtectedAccountExternalIDs = append(ids[:dynamicMonitorMaxAccounts:dynamicMonitorMaxAccounts], 1, 0, -1)
	config = config.Normalize()
	if err := config.Validate(); err != nil {
		t.Fatalf("limit must apply after filtering and deduplication: %v", err)
	}
}

func TestDynamicMonitorProtectionOverridesEveryCloseRule(t *testing.T) {
	for _, reason := range []string{dynamicMonitorReasonFirstToken, dynamicMonitorReasonSingleFirstToken, dynamicMonitorReasonError} {
		t.Run(reason, func(t *testing.T) {
			group := ruleGroup(1, 2, 11, 12)
			if reason == dynamicMonitorReasonSingleFirstToken {
				group.SingleTimeoutSeconds = 3
				group.ConsecutiveCount = 100
			}
			config := DynamicMonitorConfig{
				Enabled:                     true,
				Groups:                      []DynamicMonitorRuleGroup{group},
				ProtectedAccountExternalIDs: []int64{11, 15},
			}.Normalize()
			accounts := []storage.RelayAccount{
				{ID: 1, ExternalID: 11, Schedulable: true, Status: "active"},
				{ID: 2, ExternalID: 12, Schedulable: true, Status: "active"},
				{ID: 3, ExternalID: 13, Schedulable: false, Status: "active"},
				{ID: 4, ExternalID: 14, Schedulable: true, Status: "error"},
				{ID: 5, ExternalID: 15, Schedulable: true, Status: "active"},
			}
			hits := make(map[int64]dynamicMonitorErrorHit)
			for i := range accounts {
				if reason == dynamicMonitorReasonError {
					hits[accounts[i].ExternalID] = dynamicMonitorErrorHit{Count: 5}
				} else {
					accounts[i].LatencySamplesJSON = latencyBody(t, 5000, 2, time.Now().UTC())
				}
			}
			plan := planDynamicMonitorWithErrors(config, DynamicMonitorState{}, accounts, nil, nil, hits)
			if !slices.Equal(plan.Close, []int64{12}) || plan.CloseReasons[12] != reason {
				t.Fatalf("close = %v, reasons = %v", plan.Close, plan.CloseReasons)
			}
			for _, id := range []int64{11, 15} {
				if _, ok := plan.CloseReasons[id]; ok {
					t.Fatalf("protected account %d has a closure reason", id)
				}
				if _, ok := plan.CloseErrorHits[id]; ok {
					t.Fatalf("protected account %d has an error hit", id)
				}
			}
			config.ProtectedAccountExternalIDs = nil
			unprotected := planDynamicMonitorWithErrors(config, DynamicMonitorState{}, accounts, nil, nil, hits)
			if !slices.Contains(unprotected.Close, 11) {
				t.Fatal("removing protection must restore the original rule")
			}
		})
	}
}

func TestDynamicMonitorProtectionSurvivesAutomaticGroupAssignment(t *testing.T) {
	config := DynamicMonitorConfig{
		Enabled:                     true,
		Groups:                      []DynamicMonitorRuleGroup{ruleGroupWithSingleTimeout(1, 2, 3, 11)},
		ProtectedAccountExternalIDs: []int64{13},
		GroupRuleGroups:             map[string]int{"1": 0},
	}.Normalize()
	accounts := []storage.RelayAccount{
		{ID: 1, ExternalID: 11, Schedulable: true, Status: "active"},
		{ID: 3, ExternalID: 13, Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 3, time.Now().UTC())},
		{ID: 4, ExternalID: 14, Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 3, time.Now().UTC())},
	}
	groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{{RelayAccountID: 1, RelayGroupID: 1}, {RelayAccountID: 3, RelayGroupID: 1}, {RelayAccountID: 4, RelayGroupID: 1}}
	state := DynamicMonitorState{SeenAccounts: map[int64]struct{}{11: {}}}
	hits := map[int64]dynamicMonitorErrorHit{13: {Count: 3}}
	plan := planDynamicMonitorWithErrors(config, state, accounts, groups, links, hits)
	if !slices.Contains(plan.Config.Groups[0].AccountExternalIDs, 13) || !slices.Equal(plan.Close, []int64{14}) {
		t.Fatalf("auto-assigned protection failed: config = %+v, close = %v", plan.Config, plan.Close)
	}
	if _, seen := plan.State.SeenAccounts[13]; !seen {
		t.Fatal("protection must preserve automatic assignment bookkeeping")
	}

	// Explicit additions (including clones) use another assignment path.
	assigned := planRuleGroupAssignments(config, []int64{13, 14}, accounts, groups, links)
	config.Groups[0].AccountExternalIDs = append(slices.Clone(config.Groups[0].AccountExternalIDs), assigned[0]...)
	plan = planDynamicMonitorWithErrors(config, state, accounts, groups, links, hits)
	if !slices.Equal(plan.Close, []int64{14}) {
		t.Fatalf("explicitly assigned protection failed: %v", plan.Close)
	}
}

func TestDynamicMonitorProtectionDoesNotReopenManualClosuresOrDiscardTrackedOnes(t *testing.T) {
	closedAt := time.Now().UTC().Add(-time.Hour)
	config := DynamicMonitorConfig{
		Enabled:                     true,
		Groups:                      []DynamicMonitorRuleGroup{ruleGroup(1, 2, 11)},
		ProtectedAccountExternalIDs: []int64{11, 12},
	}.Normalize()
	accounts := []storage.RelayAccount{
		{ID: 1, ExternalID: 11, Schedulable: false, Status: "active"},
		{ID: 2, ExternalID: 12, Schedulable: false, Status: "active"},
	}
	closure := DynamicMonitorClosure{AccountExternalID: 11, ClosedAt: closedAt, RuleCategory: dynamicMonitorRuleCategoryMonitor}
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{closure}}
	plan := planDynamicMonitor(config, state, accounts, nil, nil)
	if len(plan.Close) != 0 || len(plan.Reopen) != 0 || len(plan.State.ClosedAccounts) != 1 || !reflect.DeepEqual(plan.State.ClosedAccounts[0], closure) {
		t.Fatalf("adding protection changed existing scheduling/records: %+v", plan)
	}
	config.MinAccountsPerGroup = 2
	groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{{RelayAccountID: 1, RelayGroupID: 1}, {RelayAccountID: 2, RelayGroupID: 1}}
	plan = planDynamicMonitor(config, state, accounts, groups, links)
	if !slices.Equal(plan.Reopen, []int64{11}) {
		t.Fatalf("group floor must still only reopen tracked closures, not manual account 12: %v", plan.Reopen)
	}
}

func TestDynamicMonitorErrorGroupEnabledJSONCompatibility(t *testing.T) {
	for _, test := range []struct {
		name    string
		field   string
		enabled bool
	}{
		{name: "legacy", enabled: true},
		{name: "null", field: `,"enabled":null`, enabled: true},
		{name: "enabled", field: `,"enabled":true`, enabled: true},
		{name: "disabled", field: `,"enabled":false`, enabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := `{"enabled":true,"error_groups":[{"name":"errors","status_codes":[503,502],"threshold":2,"lookback_minutes":10,"allow_auto_open":true,"auto_open_minutes":30` + test.field + `}]}`
			var direct DynamicMonitorConfig
			if err := json.Unmarshal([]byte(raw), &direct); err != nil {
				t.Fatal(err)
			}
			if direct.ErrorGroups[0].IsEnabled() != test.enabled {
				t.Fatal("API JSON decoding changed enabled semantics")
			}
			config := ParseDynamicMonitorConfig(raw)
			group := config.ErrorGroups[0]
			if group.Enabled == nil || group.IsEnabled() != test.enabled || !group.AllowAutoOpen || group.AutoOpenMinutes != 30 {
				t.Fatalf("normalization lost enabled or auto-open settings: %+v", group)
			}
			if err := config.Validate(); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(config.Normalize())
			if err != nil {
				t.Fatal(err)
			}
			restored := ParseDynamicMonitorConfig(string(body))
			if len(restored.ErrorGroups) != 1 || restored.ErrorGroups[0].IsEnabled() != test.enabled || restored.ErrorGroups[0].ID != group.ID {
				t.Fatalf("save/parse changed enabled or stable ID: %s", body)
			}
		})
	}
	if !(DynamicMonitorErrorRuleGroup{}).IsEnabled() {
		t.Fatal("old in-memory rules must also default to enabled")
	}
}

func TestDynamicMonitorErrorGroupIDsStayStableAndRejectDuplicates(t *testing.T) {
	group := DynamicMonitorErrorRuleGroup{Name: "same", StatusCodes: []int{502}, Threshold: 1, LookbackMinutes: 10}
	config := DynamicMonitorConfig{Enabled: true, ErrorGroups: []DynamicMonitorErrorRuleGroup{group, group}}.Normalize()
	if config.ErrorGroups[0].ID == "" || config.ErrorGroups[0].ID == config.ErrorGroups[1].ID {
		t.Fatal("identical groups still need distinct identities")
	}
	originalID := config.ErrorGroups[0].ID
	config.ErrorGroups[0].Name = "renamed"
	config.ErrorGroups[0].Enabled = dynamicMonitorEnabled(false)
	slices.Reverse(config.ErrorGroups)
	config = config.Normalize()
	if config.ErrorGroups[1].ID != originalID {
		t.Fatal("rename, disable or reorder changed the rule identity")
	}
	withoutIDs := DynamicMonitorConfig{ErrorGroups: []DynamicMonitorErrorRuleGroup{group}}.Normalize()
	group.Enabled = dynamicMonitorEnabled(false)
	disabled := DynamicMonitorConfig{ErrorGroups: []DynamicMonitorErrorRuleGroup{group}}.Normalize()
	if withoutIDs.ErrorGroups[0].ID != disabled.ErrorGroups[0].ID {
		t.Fatal("legacy IDs must not depend on enabled")
	}
	config.ErrorGroups[0].ID = config.ErrorGroups[1].ID
	if err := config.Validate(); err == nil {
		t.Fatal("duplicate stable IDs must be rejected")
	}
	config.ErrorGroups[0].ID = strings.Repeat("x", 129)
	if err := config.Validate(); err == nil {
		t.Fatal("oversized stable IDs must be rejected")
	}
}

func TestDynamicMonitorDisabledErrorGroupOnlyBlocksItsOwnHits(t *testing.T) {
	config := DynamicMonitorConfig{
		Enabled: true,
		Groups:  []DynamicMonitorRuleGroup{ruleGroup(1, 2, 11)},
		ErrorGroups: []DynamicMonitorErrorRuleGroup{
			{ID: "disabled", Name: "same", Enabled: dynamicMonitorEnabled(false), StatusCodes: []int{502}},
			{ID: "enabled", Name: "same", StatusCodes: []int{502}},
		},
	}.Normalize()
	accounts := []storage.RelayAccount{
		{ExternalID: 11, Schedulable: true, Status: "active", LatencySamplesJSON: latencyBody(t, 5000, 2, time.Now().UTC())},
		{ExternalID: 12, Schedulable: true, Status: "active"},
		{ExternalID: 13, Schedulable: true, Status: "active"},
	}
	hits := map[int64]dynamicMonitorErrorHit{
		11: {RuleGroupID: "disabled", Count: 3},
		12: {RuleGroupID: "disabled", Count: 3},
		13: {RuleGroupID: "enabled", Count: 3},
	}
	plan := planDynamicMonitorWithErrors(config, DynamicMonitorState{}, accounts, nil, nil, hits)
	if !slices.Equal(plan.Close, []int64{11, 13}) || plan.CloseReasons[11] != dynamicMonitorReasonFirstToken || plan.CloseReasons[13] != dynamicMonitorReasonError {
		t.Fatalf("disabled group affected other rules: close = %v, reasons = %v", plan.Close, plan.CloseReasons)
	}
}

func TestDynamicMonitorDisabledErrorGroupPausesOnlyItsOwnDeadline(t *testing.T) {
	closedAt := time.Now().UTC().Add(-time.Hour)
	deadline := closedAt.Add(10 * time.Minute)
	config := DynamicMonitorConfig{
		Enabled: true, AllowAutoOpen: true, AutoOpenMinutes: 20,
		ErrorGroups: []DynamicMonitorErrorRuleGroup{
			{ID: "disabled", Name: "renamed", Enabled: dynamicMonitorEnabled(false), StatusCodes: []int{502}, LookbackMinutes: 10},
			{ID: "enabled", Name: "renamed", StatusCodes: []int{502}, LookbackMinutes: 10},
			{ID: "legacy", Name: "legacy", Enabled: dynamicMonitorEnabled(false), StatusCodes: []int{503}, LookbackMinutes: 5},
		},
	}.Normalize()
	for _, test := range []struct {
		name     string
		closure  DynamicMonitorClosure
		expected bool
	}{
		{name: "disabled ID after rename", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupID: "disabled", RuleGroupName: "old name"}},
		{name: "enabled same name and conditions", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupID: "enabled", RuleGroupName: "renamed"}, expected: true},
		{name: "monitor same name", closure: DynamicMonitorClosure{RuleCategory: "monitor", RuleGroupName: "renamed"}, expected: true},
		{name: "legacy disabled", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupName: "legacy", ErrorStatusCodes: []int{503}, ErrorWindowMinutes: 5}},
		{name: "legacy different status", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupName: "legacy", ErrorStatusCodes: []int{502}, ErrorWindowMinutes: 5}, expected: true},
		{name: "legacy different window", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupName: "legacy", ErrorStatusCodes: []int{503}, ErrorWindowMinutes: 10}, expected: true},
		{name: "legacy reason only", closure: DynamicMonitorClosure{Reason: dynamicMonitorReasonError, RuleGroupName: "legacy", ErrorStatusCodes: []int{503}, ErrorWindowMinutes: 5}},
		{name: "deleted ID must not match replacement name", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupID: "deleted", RuleGroupName: "legacy"}, expected: true},
		{name: "deleted legacy rule", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupName: "deleted"}, expected: true},
		{name: "legacy same-name keeps first-match priority", closure: DynamicMonitorClosure{RuleCategory: "error", RuleGroupName: "renamed", ErrorStatusCodes: []int{502}, ErrorWindowMinutes: 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			closure := test.closure
			closure.AccountExternalID = 11
			closure.ClosedAt = closedAt
			closure.AutoOpenAt = &deadline
			got := dynamicMonitorClosureDeadline(closure, config)
			if (got != nil) != test.expected || (got != nil && !got.Equal(deadline)) {
				t.Fatalf("deadline = %v, expected enabled = %v", got, test.expected)
			}
			body, err := json.Marshal(DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{closure}})
			if err != nil {
				t.Fatal(err)
			}
			state := ParseDynamicMonitorState(string(body))
			plan := planDynamicMonitor(config, state, []storage.RelayAccount{{ExternalID: 11, Status: "active"}}, nil, nil)
			if len(plan.State.ClosedAccounts) != 1 || plan.State.ClosedAccounts[0].RuleGroupID != closure.RuleGroupID || len(plan.Reopen) != 0 {
				t.Fatalf("disabling must retain closure and must not reopen immediately: %+v", plan)
			}
		})
	}
	closure := DynamicMonitorClosure{RuleCategory: "error", RuleGroupID: "disabled", ClosedAt: closedAt, AutoOpenAt: &deadline}
	config.ErrorGroups[0].Enabled = dynamicMonitorEnabled(true)
	if got := dynamicMonitorClosureDeadline(closure, config); got == nil || !got.Equal(deadline) {
		t.Fatal("re-enabling must resume the saved deadline")
	}
}

func TestDynamicMonitorErrorFetchingSkipsDisabledGroupsAndProtectedAccounts(t *testing.T) {
	var requestedCodes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedCodes = append(requestedCodes, r.URL.Query().Get("status_codes"))
		if r.URL.Path != "/api/v1/admin/ops/request-errors" || r.URL.Query().Get("status_codes") != "503" {
			t.Errorf("disabled group fetched errors: %s", r.URL)
		}
		items := []remoteRequestError{
			{AccountID: 11, StatusCode: 503, ErrorOwner: "provider", Message: "failed", CreatedAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339)},
			{AccountID: 12, StatusCode: 503, ErrorOwner: "provider", Message: "failed", CreatedAt: time.Now().UTC().Add(-time.Second).Format(time.RFC3339)},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": items, "total": 2}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	cipher, err := crypto.NewCipher("dynamic-protection-test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := cipher.Encrypt("test-key")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(nil, cipher)
	station := &storage.RelayStation{BaseURL: server.URL, APIKeyCipher: key}
	accounts := []storage.RelayAccount{
		{ExternalID: 11, Status: "active", Schedulable: true},
		{ExternalID: 12, Status: "active", Schedulable: true},
	}
	config := DynamicMonitorConfig{
		ProtectedAccountExternalIDs: []int64{11},
		ErrorGroups: []DynamicMonitorErrorRuleGroup{
			{ID: "off", Name: "disabled", Enabled: dynamicMonitorEnabled(false), StatusCodes: []int{502}, Threshold: 1},
			{ID: "on", Name: "enabled", StatusCodes: []int{503}, Threshold: 1, AllowAutoOpen: true, AutoOpenMinutes: 5},
		},
	}.Normalize()
	hits := svc.dynamicMonitorErrorHits(context.Background(), station, config, DynamicMonitorState{}, accounts)
	if !slices.Equal(requestedCodes, []string{"503"}) || len(hits) != 1 || hits[12].RuleGroupID != "on" || !hits[12].AllowAutoOpen {
		t.Fatalf("requests = %v, hits = %+v", requestedCodes, hits)
	}
	config.ErrorGroups[1].Enabled = dynamicMonitorEnabled(false)
	if got := NewService(nil, nil).dynamicMonitorErrorHits(context.Background(), station, config, DynamicMonitorState{}, accounts); len(got) != 0 {
		t.Fatalf("all disabled groups must skip fetching/decryption: %+v", got)
	}
	if _, complete := svc.fetchDynamicMonitorErrorEvents(context.Background(), server.URL, "test-key", config.ErrorGroups[0], time.Now().Add(-time.Hour), time.Now()); complete {
		t.Fatal("disabled groups must not fetch even when called directly")
	}
	config.ErrorGroups[1].Enabled = dynamicMonitorEnabled(true)
	config.ProtectedAccountExternalIDs = []int64{11, 12}
	if got := svc.dynamicMonitorErrorHits(context.Background(), station, config, DynamicMonitorState{}, accounts); len(got) != 0 {
		t.Fatalf("all protected accounts must skip fetching: %+v", got)
	}
	if !slices.Equal(requestedCodes, []string{"503"}) {
		t.Fatalf("unexpected extra requests: %v", requestedCodes)
	}
}

func TestDynamicMonitorBindsLegacyErrorClosuresBeforeConfigEdits(t *testing.T) {
	config := DynamicMonitorConfig{ErrorGroups: []DynamicMonitorErrorRuleGroup{
		{Name: "same", StatusCodes: []int{502}, LookbackMinutes: 10},
		{Name: "same", StatusCodes: []int{502}, LookbackMinutes: 10},
	}}.Normalize()
	deadline := time.Now().UTC().Add(time.Hour)
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{
		{AccountExternalID: 1, RuleCategory: "error", RuleGroupName: "same", ErrorStatusCodes: []int{502}, ErrorWindowMinutes: 10, AutoOpenAt: &deadline},
		{AccountExternalID: 2, RuleCategory: "monitor", RuleGroupName: "same", AutoOpenAt: &deadline},
		{AccountExternalID: 3, RuleCategory: "error", RuleGroupID: config.ErrorGroups[1].ID, AutoOpenAt: &deadline},
		{AccountExternalID: 4, RuleCategory: "error", RuleGroupName: "deleted", AutoOpenAt: &deadline},
	}}
	next, changed := bindDynamicMonitorErrorRuleIDs(state, config)
	if !changed || next.ClosedAccounts[0].RuleGroupID != config.ErrorGroups[0].ID || state.ClosedAccounts[0].RuleGroupID != "" {
		t.Fatalf("legacy binding lost first-match priority or mutated input: %+v", next)
	}
	if !reflect.DeepEqual(next.ClosedAccounts[1:], state.ClosedAccounts[1:]) || next.ClosedAccounts[0].AutoOpenAt != &deadline {
		t.Fatal("binding changed unrelated rules or saved deadlines")
	}
	config.ErrorGroups[0].Name = "renamed"
	config.ErrorGroups[0].StatusCodes = []int{429}
	config.ErrorGroups[0].Enabled = dynamicMonitorEnabled(false)
	slices.Reverse(config.ErrorGroups)
	if dynamicMonitorClosureDeadline(next.ClosedAccounts[0], config) != nil {
		t.Fatal("legacy closure was not paused after rename, edit and reorder")
	}
	for _, closure := range next.ClosedAccounts[1:] {
		if dynamicMonitorClosureDeadline(closure, config) == nil {
			t.Fatal("binding paused an unrelated closure")
		}
	}
	if _, changed := bindDynamicMonitorErrorRuleIDs(next, config); changed {
		t.Fatal("binding must be idempotent")
	}
}

func TestDynamicMonitorDisabledErrorGroupDoesNotDisableGroupFloor(t *testing.T) {
	config := DynamicMonitorConfig{
		Enabled: true, MinAccountsPerGroup: 1,
		ErrorGroups: []DynamicMonitorErrorRuleGroup{{ID: "off", Enabled: dynamicMonitorEnabled(false), StatusCodes: []int{502}}},
	}.Normalize()
	closure := DynamicMonitorClosure{AccountExternalID: 11, RuleCategory: "error", RuleGroupID: "off", ClosedAt: time.Now().UTC().Add(-time.Hour)}
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{closure}}
	accounts := []storage.RelayAccount{{ID: 1, ExternalID: 11, Status: "active"}}
	groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Status: "active"}}
	links := []storage.RelayAccountGroup{{RelayAccountID: 1, RelayGroupID: 1}}
	plan := planDynamicMonitor(config, state, accounts, groups, links)
	if !slices.Equal(plan.Reopen, []int64{11}) || len(plan.State.ClosedAccounts) != 1 {
		t.Fatalf("disabled error group changed independent floor strategy: %+v", plan)
	}
}

func TestDynamicMonitorLegacyErrorClosureMatchesReachedThresholdOnly(t *testing.T) {
	config := DynamicMonitorConfig{ErrorGroups: []DynamicMonitorErrorRuleGroup{
		{ID: "high", Name: "same", StatusCodes: []int{502}, Threshold: 10, LookbackMinutes: 5, Enabled: dynamicMonitorEnabled(false)},
		{ID: "low", Name: "same", StatusCodes: []int{502}, Threshold: 2, LookbackMinutes: 5},
	}}.Normalize()
	deadline := time.Now().UTC().Add(-time.Minute)
	closure := DynamicMonitorClosure{RuleCategory: "error", RuleGroupName: "same", ErrorStatusCodes: []int{502}, ErrorWindowMinutes: 5, ErrorCount: 3, AutoOpenAt: &deadline}
	if dynamicMonitorClosureDeadline(closure, config) == nil {
		t.Fatal("disabled high-threshold rule must not pause a closure caused by the enabled low-threshold rule")
	}
	next, changed := bindDynamicMonitorErrorRuleIDs(DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{closure}}, config)
	if !changed || next.ClosedAccounts[0].RuleGroupID != "low" {
		t.Fatalf("legacy binding ignored the recorded error count: %+v", next)
	}
}
