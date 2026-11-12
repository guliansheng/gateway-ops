package relay

import (
	"math"
	"reflect"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestEvaluateAccountRiskProfitTolerance(t *testing.T) {
	// Keep the addition at runtime so this includes ordinary binary float drift,
	// rather than Go's exact constant arithmetic.
	oneTenth, twoTenths := 0.1, 0.2
	sum := oneTenth + twoTenths
	tests := []struct {
		name     string
		cost     float64
		sale     float64
		state    string
		zero     bool
		noProfit bool
		unsafe   bool
	}{
		{name: "decimal addition above cost", cost: 0.3, sale: sum, state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "decimal addition below cost", cost: sum, sale: 0.3, state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "positive difference inside tolerance", cost: 1, sale: 1 + 5e-10, state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "negative difference inside tolerance", cost: 1, sale: 1 - 5e-10, state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "profit outside tolerance", cost: 1, sale: 1 + 2e-9, state: RiskStateProtected},
		{name: "loss outside tolerance", cost: 1, sale: 1 - 2e-9, state: RiskStateNoSafeCandidate, unsafe: true},
		{name: "clear profit", cost: 0.3, sale: 0.4, state: RiskStateProtected},
		{name: "clear loss", cost: 0.3, sale: 0.2, state: RiskStateNoSafeCandidate, unsafe: true},
		{name: "zero cost and zero sale", cost: 0, sale: 0, state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "zero cost and negative zero sale", cost: 0, sale: math.Copysign(0, -1), state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "negative zero cost and zero sale", cost: math.Copysign(0, -1), sale: 0, state: RiskStateNoProfit, zero: true, noProfit: true},
		{name: "zero cost and positive sale", cost: 0, sale: 0.4, state: RiskStateProtected},
		{name: "zero cost and near zero sale", cost: 0, sale: 5e-10, state: RiskStateNoProfit, zero: true, noProfit: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cost := test.cost
			account := storage.RelayAccount{ExternalID: 8, Type: "apikey", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
			groups := []storage.RelayGroup{{ID: 1, ExternalID: 1, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: test.sale}}
			risk := evaluateAccountRisk(account, groups, groups, 0)
			if risk.State != test.state {
				t.Errorf("state = %q, want %q", risk.State, test.state)
			}
			if risk.CurrentMinMultiplier == nil || *risk.CurrentMinMultiplier != test.sale {
				t.Fatalf("current minimum = %v, want %.17g", risk.CurrentMinMultiplier, test.sale)
			}
			if risk.Margin == nil {
				t.Fatal("known active cost and sale must have a margin")
			}
			if test.zero {
				if *risk.Margin != 0 || math.Signbit(*risk.Margin) {
					t.Errorf("break-even margin = %.17g, want positive zero", *risk.Margin)
				}
			} else if want := test.sale - test.cost; *risk.Margin != want {
				t.Errorf("margin = %.17g, want %.17g", *risk.Margin, want)
			}
			if got := len(risk.NoProfitGroups) == 1; got != test.noProfit {
				t.Errorf("no-profit groups = %v, want no-profit=%v", groupIDs(risk.NoProfitGroups), test.noProfit)
			}
			if got := len(risk.UnsafeGroups) == 1; got != test.unsafe {
				t.Errorf("unsafe groups = %v, want unsafe=%v", groupIDs(risk.UnsafeGroups), test.unsafe)
			}
			if risk.RecommendedGroup != nil || risk.CanApply {
				t.Errorf("no profitable alternative exists: recommended=%v canApply=%v", risk.RecommendedGroup, risk.CanApply)
			}
		})
	}
}

func TestEvaluateAccountRiskRejectsBreakEvenProfitCandidate(t *testing.T) {
	oneTenth, twoTenths := 0.1, 0.2
	nearBreakEven := oneTenth + twoTenths
	for _, current := range []struct {
		name  string
		sale  float64
		state string
	}{
		{name: "current group itself is break-even", sale: nearBreakEven, state: RiskStateNoProfit},
		{name: "current group is loss-making", sale: 0.2, state: RiskStateRisk},
	} {
		for _, restrict := range []bool{false, true} {
			name := current.name + "/display"
			if restrict {
				name = current.name + "/automatic"
			}
			t.Run(name, func(t *testing.T) {
				cost := 0.3
				account := storage.RelayAccount{ExternalID: 8, Type: "apikey", ModelType: "gpt", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "upstream_probe"}
				groups := []storage.RelayGroup{
					{ID: 1, ExternalID: 1, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: current.sale},
					{ID: 2, ExternalID: 2, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: nearBreakEven},
					{ID: 3, ExternalID: 3, Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.4},
				}
				risk := evaluateAccountRiskWithOptions(account, groups[:1], groups, nil, restrict)
				if risk.State != current.state || !risk.CanApply {
					t.Errorf("state=%q canApply=%v, want %q / true", risk.State, risk.CanApply, current.state)
				}
				if risk.RecommendedGroup == nil || risk.RecommendedGroup.ExternalID != 3 {
					t.Errorf("recommended = %#v, want genuinely profitable group 3", risk.RecommendedGroup)
				}
				if got, want := risk.SuggestedGroupIDs, []int64{3}; !reflect.DeepEqual(got, want) {
					t.Errorf("suggested IDs = %v, want %v", got, want)
				}
				if risk.RecommendedGroup != nil {
					next := []storage.RelayGroup{*risk.RecommendedGroup}
					after := evaluateAccountRiskWithOptions(account, next, groups, nil, restrict)
					if after.State != RiskStateProtected {
						t.Errorf("recommended group's resulting state = %q, want protected", after.State)
					}
				}
			})
		}
	}
}

func TestEvaluateAccountRiskProfitMinimumUsesOnlyEligibleCurrentGroups(t *testing.T) {
	cost := 0.3
	account := storage.RelayAccount{ExternalID: 8, Type: "apikey", ModelType: "gpt", Platform: "openai", Status: "active", RateMultiplier: &cost, RateSource: "manual"}
	groups := []storage.RelayGroup{
		{ID: 1, ExternalID: 1, Name: "first by name", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.8},
		// Current sales-group risk intentionally does not require a model-type
		// match; only automatic candidates and downgrades have that restriction.
		{ID: 2, ExternalID: 2, Name: "minimum", Platform: "openai", Status: "active", ModelTypesJSON: `["claude"]`, RateMultiplier: 0.4},
		{ID: 3, ExternalID: 3, Name: "inactive", Platform: "openai", Status: "inactive", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.1},
		{ID: 4, ExternalID: 4, Name: "different platform", Platform: "anthropic", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.1},
		{ID: 5, ExternalID: 5, Name: "OAuth only", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RequireOAuthOnly: true, RateMultiplier: 0.1},
		{ID: 6, ExternalID: 6, Name: "different account type", Platform: "openai", Status: "active", ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.1},
	}
	groupTypes := map[uint]map[string]struct{}{6: {"oauth": {}}}
	risk := evaluateAccountRiskWithOptions(account, groups, groups, groupTypes, false)
	if risk.State != RiskStateProtected || risk.CurrentMinMultiplier == nil || *risk.CurrentMinMultiplier != 0.4 {
		t.Fatalf("state=%q minimum=%v, want protected / 0.4", risk.State, risk.CurrentMinMultiplier)
	}
	if risk.Margin == nil || *risk.Margin != 0.4-cost {
		t.Fatalf("margin = %v, want %.17g", risk.Margin, 0.4-cost)
	}
	if len(risk.CurrentGroups) != len(groups) {
		t.Fatalf("displayed assignments = %d, want %d", len(risk.CurrentGroups), len(groups))
	}
}

func TestEvaluateAccountRiskProfitUnavailableBoundaries(t *testing.T) {
	cost, negative := 0.3, -0.1
	for _, test := range []struct {
		name   string
		status string
		cost   *float64
		source string
		group  string
		state  string
	}{
		{name: "unknown multiplier", status: "active", source: "upstream_probe", group: "active", state: RiskStateCostUnknown},
		{name: "negative multiplier", status: "active", cost: &negative, source: "manual", group: "active", state: RiskStateCostUnknown},
		{name: "unproven default multiplier", status: "active", cost: &cost, group: "active", state: RiskStateCostUnknown},
		{name: "inactive account", status: "inactive", cost: &cost, source: "upstream_probe", group: "active", state: RiskStateInactive},
		{name: "inactive sales group", status: "active", cost: &cost, source: "upstream_probe", group: "inactive", state: RiskStateUnassigned},
		{name: "no sales group", status: "active", cost: &cost, source: "upstream_probe", state: RiskStateUnassigned},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := storage.RelayAccount{ExternalID: 8, Type: "apikey", Platform: "openai", Status: test.status, RateMultiplier: test.cost, RateSource: test.source}
			var groups []storage.RelayGroup
			if test.group != "" {
				groups = []storage.RelayGroup{{ID: 1, ExternalID: 1, Platform: "openai", Status: test.group, ModelTypesJSON: `["gpt"]`, RateMultiplier: 0.4}}
			}
			risk := evaluateAccountRisk(account, groups, groups, 0)
			if risk.State != test.state || risk.Margin != nil || risk.CurrentMinMultiplier != nil || risk.CanApply {
				t.Fatalf("state=%q margin=%v minimum=%v canApply=%v, want %q with no profit assessment", risk.State, risk.Margin, risk.CurrentMinMultiplier, risk.CanApply, test.state)
			}
		})
	}
}
