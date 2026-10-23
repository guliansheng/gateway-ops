package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"testing"
	"time"
)

func accountClearSteps(leaseUntil driver.Value, extra ...degradationSQLStep) []degradationSQLStep {
	steps := append(accountDegradationScopeSteps(), degradationSQLStep{
		contains: "FOR UPDATE",
		columns:  []string{"id", "station_id", "account_external_id", "lease_token", "lease_until"},
		rows:     [][]driver.Value{{int64(7), int64(1), int64(11), "lease", leaseUntil}},
	})
	return append(steps, extra...)
}

func TestClearAccountDegradationHistoryHonoursRunLease(t *testing.T) {
	t.Run("running test blocks clearing", func(t *testing.T) {
		repo, state := newDegradationSQL(t, accountClearSteps(time.Now().Add(time.Minute))...)
		err := repo.ClearAccountDegradationHistory(context.Background(), 1, 11)
		if !errors.Is(err, ErrGroupDegradationBusy) || state.rollbacks != 1 || state.commits != 0 {
			t.Fatalf("err=%v state=%+v", err, state)
		}
	})
	t.Run("idle scope deletes every record", func(t *testing.T) {
		deleteStep := degradationSQLStep{contains: `DELETE FROM "account_degradation_results"`, check: func(_ string, args []driver.NamedValue) {
			if len(args) != 2 || args[0].Value != int64(1) || args[1].Value != int64(11) {
				t.Errorf("unscoped delete args=%+v", args)
			}
		}}
		repo, state := newDegradationSQL(t, accountClearSteps(time.Now().Add(-time.Minute), deleteStep)...)
		if err := repo.ClearAccountDegradationHistory(context.Background(), 1, 11); err != nil {
			t.Fatalf("clear error = %v", err)
		}
		if state.commits != 1 || state.rollbacks != 0 {
			t.Fatalf("state=%+v", state)
		}
	})
	t.Run("scope without run row still clears", func(t *testing.T) {
		steps := append(accountDegradationScopeSteps(),
			degradationSQLStep{contains: "FOR UPDATE", columns: []string{"id"}, rows: nil},
			degradationSQLStep{contains: `DELETE FROM "account_degradation_results"`},
		)
		repo, _ := newDegradationSQL(t, steps...)
		if err := repo.ClearAccountDegradationHistory(context.Background(), 1, 11); err != nil {
			t.Fatalf("clear error = %v", err)
		}
	})
}

// usageRowColumns mirrors the column list DegradationUsage selects from both
// result tables; account rows append their scope id.
var usageRowColumns = []string{"id", "method", "model", "judge_model", "status", "created_at", "used_account_id", "used_account_name", "input_tokens", "output_tokens", "base_cost", "cost_multiplier", "actual_cost"}

func usageRow(id int64, createdAt time.Time, status string, accountID int64, name string) []driver.Value {
	return []driver.Value{id, "candy", "candidate", "candidate", status, createdAt, accountID, name, int64(100), int64(20), 0.002, 0.8, 0.0016}
}

func usageAccountRow(id int64, createdAt time.Time, accountExternalID int64) []driver.Value {
	return append(usageRow(id, createdAt, "normal", 502, "上游账号B"), accountExternalID)
}

func usageTotalsStep(runs, input, output int64, base, actual float64) degradationSQLStep {
	return degradationSQLStep{contains: `COUNT(*) AS runs`, columns: []string{"runs", "input_tokens", "output_tokens", "base_cost", "actual_cost"}, rows: [][]driver.Value{{runs, input, output, base, actual}}}
}

func usageGroupStep(t *testing.T, window int64, rows [][]driver.Value) degradationSQLStep {
	t.Helper()
	scoped := make([][]driver.Value, 0, len(rows))
	for _, row := range rows {
		scoped = append(scoped, append(append([]driver.Value{}, row...), int64(9)))
	}
	return degradationSQLStep{contains: `FROM "group_degradation_results"`, columns: append(append([]string{}, usageRowColumns...), "group_external_id"), rows: scoped, check: func(_ string, args []driver.NamedValue) {
		if window > 0 && args[len(args)-1].Value != window {
			t.Errorf("group window args=%+v", args)
		}
	}}
}
func usageAccountStep(rows [][]driver.Value) degradationSQLStep {
	return degradationSQLStep{contains: `FROM "account_degradation_results"`, columns: append(append([]string{}, usageRowColumns...), "account_external_id"), rows: rows}
}

func usageNameSteps() []degradationSQLStep {
	return []degradationSQLStep{
		{contains: `FROM "relay_groups"`, columns: []string{"external_id", "name"}, rows: [][]driver.Value{{int64(9), "甲组"}}},
		{contains: `FROM "relay_accounts"`, columns: []string{"external_id", "name"}, rows: [][]driver.Value{{int64(11), "测试账号"}}},
	}
}

func usageStationStep() degradationSQLStep {
	return degradationSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}}
}

func TestDegradationUsageMergesBothScopesNewestFirst(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	groupRows := [][]driver.Value{
		usageRow(2, base.Add(2*time.Minute), "normal", 501, "上游账号A"),
		usageRow(1, base.Add(time.Minute), "degraded", 0, ""),
	}
	accountRows := [][]driver.Value{usageAccountRow(5, base.Add(3*time.Minute), 11)}
	steps := append([]degradationSQLStep{usageStationStep(), usageGroupStep(t, 3, groupRows), usageAccountStep(accountRows), usageTotalsStep(2, 200, 40, 0.004, 0.0032), usageTotalsStep(1, 100, 20, 0.001, 0.001)}, usageNameSteps()...)
	repo, _ := newDegradationSQL(t, steps...)
	usage, err := repo.DegradationUsage(context.Background(), 1, DegradationUsageScopeAll, 1, 3)
	if err != nil {
		t.Fatalf("usage error = %v", err)
	}
	if len(usage.Items) != 3 {
		t.Fatalf("items=%+v", usage.Items)
	}
	if usage.Items[0].Scope != DegradationUsageScopeAccount || usage.Items[0].ScopeName != "测试账号" || usage.Items[0].UsedAccountName != "上游账号B" {
		t.Errorf("newest row must be the account run: %+v", usage.Items[0])
	}
	if usage.Items[1].Scope != DegradationUsageScopeGroup || usage.Items[1].ScopeName != "甲组" || usage.Items[2].ID != 1 {
		t.Errorf("group rows must follow newest first: %+v", usage.Items)
	}
	if usage.Totals.Runs != 3 || usage.Totals.InputTokens != 300 || usage.Totals.OutputTokens != 60 || usage.Totals.BaseCost != 0.005 || math.Abs(usage.Totals.ActualCost-0.0042) > 1e-9 {
		t.Errorf("totals=%+v", usage.Totals)
	}
	if usage.Total != 3 || usage.Page != 1 || usage.PageSize != 3 || usage.Pages != 1 {
		t.Errorf("page=%+v", usage)
	}
}

func TestDegradationUsagePagesAndScopes(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	groupRows := [][]driver.Value{
		usageRow(2, base.Add(2*time.Minute), "normal", 501, "上游账号A"),
		usageRow(1, base.Add(time.Minute), "normal", 501, "上游账号A"),
	}
	accountRows := [][]driver.Value{usageAccountRow(5, base.Add(3*time.Minute), 11)}
	// Page two of two: the merged window is offset+pageSize rows from each side.
	steps := append([]degradationSQLStep{usageStationStep(), usageGroupStep(t, 4, groupRows), usageAccountStep(accountRows), usageTotalsStep(2, 200, 40, 0.004, 0.0032), usageTotalsStep(1, 100, 20, 0.001, 0.001)}, usageNameSteps()...)
	repo, _ := newDegradationSQL(t, steps...)
	usage, err := repo.DegradationUsage(context.Background(), 1, DegradationUsageScopeAll, 2, 2)
	if err != nil {
		t.Fatalf("usage error = %v", err)
	}
	if len(usage.Items) != 1 || usage.Items[0].ID != 1 || usage.Items[0].Scope != DegradationUsageScopeGroup {
		t.Fatalf("second page=%+v", usage.Items)
	}
	if usage.Page != 2 || usage.PageSize != 2 || usage.Pages != 2 || usage.Total != 3 {
		t.Errorf("paging metadata=%+v", usage)
	}

	groupOnly := append([]degradationSQLStep{usageStationStep(), usageGroupStep(t, 0, groupRows), usageTotalsStep(2, 200, 40, 0.004, 0.0032)}, usageNameSteps()...)
	scoped, _ := newDegradationSQL(t, groupOnly...)
	page, err := scoped.DegradationUsage(context.Background(), 1, DegradationUsageScopeGroup, 1, 20)
	if err != nil {
		t.Fatalf("scoped usage error = %v", err)
	}
	if len(page.Items) != 2 || page.Totals.Runs != 2 || page.Totals.ActualCost != 0.0032 {
		t.Fatalf("group scope leaked account rows: %+v", page)
	}
}

func TestCompleteDegradationUsageWritesAccounting(t *testing.T) {
	for _, scope := range []string{DegradationUsageScopeGroup, DegradationUsageScopeAccount} {
		table := `UPDATE "group_degradation_results"`
		if scope == DegradationUsageScopeAccount {
			table = `UPDATE "account_degradation_results"`
		}
		step := degradationSQLStep{contains: table, check: func(_ string, args []driver.NamedValue) {
			if len(args) == 0 || args[len(args)-1].Value != int64(55) {
				t.Errorf("scope %s update args=%+v", scope, args)
			}
		}}
		repo, _ := newDegradationSQL(t, step)
		err := repo.CompleteDegradationUsage(context.Background(), scope, 55, DegradationUsage{
			UsedAccountID: 501, UsedAccountName: "上游账号A", InputTokens: 120, OutputTokens: 30,
			BaseCost: 0.002, CostMultiplier: 0.8, ActualCost: 0.0016,
		})
		if err != nil {
			t.Fatalf("scope %s error = %v", scope, err)
		}
	}
}
