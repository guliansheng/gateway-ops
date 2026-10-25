package storage

import (
	"context"
	"sort"
	"time"
)

// Degradation usages are admin-only. They join every group and account test run
// with the upstream account and the charge Sub2API recorded for it, so operators
// can see what the detectors actually cost without querying the station again.
const (
	DegradationUsageMaxPageSize  = 100
	DegradationUsageScopeAll     = "all"
	DegradationUsageScopeGroup   = "group"
	DegradationUsageScopeAccount = "account"
)

type DegradationUsage struct {
	// Found reports whether any Sub2API usage row matched the run. It stays out
	// of the database; a miss only records the fallback account.
	Found           bool
	UsedAccountID   int64
	UsedAccountName string
	InputTokens     int64
	OutputTokens    int64
	BaseCost        float64
	CostMultiplier  float64
	ActualCost      float64
	SyncedAt        time.Time
}

type DegradationUsageTotals struct {
	Runs         int64   `json:"runs"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	BaseCost     float64 `json:"base_cost"`
	ActualCost   float64 `json:"actual_cost"`
}

type DegradationUsageItem struct {
	ID              uint      `json:"id"`
	Scope           string    `json:"scope"`
	ScopeExternalID int64     `json:"scope_id"`
	ScopeName       string    `json:"scope_name"`
	Method          string    `json:"method"`
	Model           string    `json:"model"`
	JudgeModel      string    `json:"judge_model"`
	Status          string    `json:"status"`
	UsedAccountID   int64     `json:"used_account_id"`
	UsedAccountName string    `json:"used_account_name"`
	InputTokens     int64     `json:"input_tokens"`
	OutputTokens    int64     `json:"output_tokens"`
	BaseCost        float64   `json:"base_cost"`
	CostMultiplier  float64   `json:"cost_multiplier"`
	ActualCost      float64   `json:"actual_cost"`
	CreatedAt       time.Time `json:"created_at"`
}

type DegradationUsagePage struct {
	Items    []DegradationUsageItem `json:"items"`
	Totals   DegradationUsageTotals `json:"totals"`
	Total    int64                  `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"page_size"`
	Pages    int                    `json:"pages"`
}

// CompleteDegradationUsage records the accounting captured after an async
// Sub2API usage lookup. It never creates rows: a result pruned by the history
// limit simply stays gone.
func (r *RelayStations) CompleteDegradationUsage(ctx context.Context, scope string, id uint, usage DegradationUsage) error {
	now := usage.SyncedAt
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"used_account_id":   usage.UsedAccountID,
		"used_account_name": usage.UsedAccountName,
		"input_tokens":      usage.InputTokens,
		"output_tokens":     usage.OutputTokens,
		"base_cost":         usage.BaseCost,
		"cost_multiplier":   usage.CostMultiplier,
		"actual_cost":       usage.ActualCost,
		"usage_synced_at":   now,
	}
	switch scope {
	case DegradationUsageScopeAccount:
		return r.db.WithContext(ctx).Model(&AccountDegradationResult{}).Where("id = ?", id).Updates(updates).Error
	default:
		return r.db.WithContext(ctx).Model(&GroupDegradationResult{}).Where("id = ?", id).Updates(updates).Error
	}
}

// DegradationUsage returns one merged, newest-first page across group and
// account test runs. Both tables are read with the same offset window and merged
// in memory so the query stays portable and independent of SQL dialect details.
func (r *RelayStations) DegradationUsage(ctx context.Context, stationID uint, scope string, page, pageSize int) (DegradationUsagePage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > DegradationUsageMaxPageSize {
		pageSize = DegradationUsageMaxPageSize
	}
	if scope != DegradationUsageScopeAccount {
		if scope != DegradationUsageScopeGroup {
			scope = DegradationUsageScopeAll
		}
	}
	result := DegradationUsagePage{Items: []DegradationUsageItem{}, Page: page, PageSize: pageSize}
	var station RelayStation
	if err := r.db.WithContext(ctx).Select("id").First(&station, stationID).Error; err != nil {
		return result, err
	}
	offset := (page - 1) * pageSize
	// A page needs at most offset+pageSize rows from each side; totals always
	// cover the whole scope so the header does not change while paging.
	window := offset + pageSize
	rows := make([]DegradationUsageItem, 0, window*2)
	if scope != DegradationUsageScopeAccount {
		var groups []GroupDegradationResult
		// Imported rows are copies of an account run that the account side
		// already reports, so the group side skips them: counting both would
		// inflate the run count of the merged report.
		if err := r.db.WithContext(ctx).Select(usageColumns+", group_external_id").Where("station_id = ? AND source_result_id = 0", stationID).
			Order("created_at DESC").Order("id DESC").Limit(window).Find(&groups).Error; err != nil {
			return result, err
		}
		for _, row := range groups {
			rows = append(rows, DegradationUsageItem{
				ID: row.ID, Scope: DegradationUsageScopeGroup, ScopeExternalID: row.GroupExternalID,
				Method: row.Method, Model: row.Model, JudgeModel: row.JudgeModel, Status: row.Status,
				UsedAccountID: row.UsedAccountID, UsedAccountName: row.UsedAccountName,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
				BaseCost: row.BaseCost, CostMultiplier: row.CostMultiplier, ActualCost: row.ActualCost,
				CreatedAt: row.CreatedAt,
			})
		}
	}
	if scope != DegradationUsageScopeGroup {
		var accounts []AccountDegradationResult
		if err := r.db.WithContext(ctx).Select(usageColumns+", account_external_id").Where("station_id = ?", stationID).
			Order("created_at DESC").Order("id DESC").Limit(window).Find(&accounts).Error; err != nil {
			return result, err
		}
		for _, row := range accounts {
			rows = append(rows, DegradationUsageItem{
				ID: row.ID, Scope: DegradationUsageScopeAccount, ScopeExternalID: row.AccountExternalID,
				Method: row.Method, Model: row.Model, JudgeModel: row.JudgeModel, Status: row.Status,
				UsedAccountID: row.UsedAccountID, UsedAccountName: row.UsedAccountName,
				InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
				BaseCost: row.BaseCost, CostMultiplier: row.CostMultiplier, ActualCost: row.ActualCost,
				CreatedAt: row.CreatedAt,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		if rows[i].Scope != rows[j].Scope {
			return rows[i].Scope < rows[j].Scope
		}
		return rows[i].ID > rows[j].ID
	})
	if offset < len(rows) {
		end := offset + pageSize
		if end > len(rows) {
			end = len(rows)
		}
		result.Items = rows[offset:end]
	}
	totals, total, err := r.degradationUsageTotals(ctx, stationID, scope)
	if err != nil {
		return result, err
	}
	result.Totals, result.Total = totals, total
	if pageSize > 0 {
		result.Pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	if err := r.degradationUsageNames(ctx, stationID, result.Items); err != nil {
		return result, err
	}
	return result, nil
}

const usageColumns = "id, method, model, judge_model, status, created_at, used_account_id, used_account_name, input_tokens, output_tokens, base_cost, cost_multiplier, actual_cost"

func (r *RelayStations) degradationUsageTotals(ctx context.Context, stationID uint, scope string) (DegradationUsageTotals, int64, error) {
	totals := DegradationUsageTotals{}
	sum := func(model any, where string) error {
		row := struct {
			Runs         int64
			InputTokens  int64
			OutputTokens int64
			BaseCost     float64
			ActualCost   float64
		}{}
		query := r.db.WithContext(ctx).Model(model).Select(
			"COUNT(*) AS runs, COALESCE(SUM(input_tokens),0) AS input_tokens, COALESCE(SUM(output_tokens),0) AS output_tokens, COALESCE(SUM(base_cost),0) AS base_cost, COALESCE(SUM(actual_cost),0) AS actual_cost",
		).Where("station_id = ?", stationID)
		if where != "" {
			query = query.Where(where)
		}
		err := query.Scan(&row).Error
		if err != nil {
			return err
		}
		totals.Runs += row.Runs
		totals.InputTokens += row.InputTokens
		totals.OutputTokens += row.OutputTokens
		totals.BaseCost += row.BaseCost
		totals.ActualCost += row.ActualCost
		return nil
	}
	if scope != DegradationUsageScopeAccount {
		if err := sum(&GroupDegradationResult{}, "source_result_id = 0"); err != nil {
			return totals, 0, err
		}
	}
	if scope != DegradationUsageScopeGroup {
		if err := sum(&AccountDegradationResult{}, ""); err != nil {
			return totals, 0, err
		}
	}
	return totals, totals.Runs, nil
}

func (r *RelayStations) degradationUsageNames(ctx context.Context, stationID uint, items []DegradationUsageItem) error {
	if len(items) == 0 {
		return nil
	}
	var groups []RelayGroup
	if err := r.db.WithContext(ctx).Select("external_id", "name").Where("relay_station_id = ?", stationID).Find(&groups).Error; err != nil {
		return err
	}
	groupNames := make(map[int64]string, len(groups))
	for _, group := range groups {
		groupNames[group.ExternalID] = group.Name
	}
	var accounts []RelayAccount
	if err := r.db.WithContext(ctx).Select("external_id", "name").Where("relay_station_id = ?", stationID).Find(&accounts).Error; err != nil {
		return err
	}
	accountNames := make(map[int64]string, len(accounts))
	for _, account := range accounts {
		accountNames[account.ExternalID] = account.Name
	}
	for i := range items {
		if items[i].Scope == DegradationUsageScopeAccount {
			items[i].ScopeName = accountNames[items[i].ScopeExternalID]
		} else {
			items[i].ScopeName = groupNames[items[i].ScopeExternalID]
		}
	}
	return nil
}
