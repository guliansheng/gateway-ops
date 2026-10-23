package storage

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func accountDegradationScopeSteps() []degradationSQLStep {
	return []degradationSQLStep{
		{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
		{contains: `FROM "relay_accounts"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(2)}}},
	}
}

func TestAccountDegradationHistoryScopedAndBounded(t *testing.T) {
	steps := append(accountDegradationScopeSteps(), degradationSQLStep{contains: `FROM "account_degradation_results"`, columns: []string{"id", "method", "status"}, rows: [][]driver.Value{{int64(4), "candy", "error"}}, check: func(query string, args []driver.NamedValue) {
		if !strings.Contains(query, "station_id = $1 AND account_external_id = $2") || !strings.Contains(query, "LIMIT $3") || args[0].Value != int64(1) || args[1].Value != int64(11) || args[2].Value != int64(20) {
			t.Errorf("unscoped history %s %+v", query, args)
		}
	}})
	repo, _ := newDegradationSQL(t, steps...)
	results, err := repo.AccountDegradationHistory(context.Background(), 1, 11)
	if err != nil || len(results) != 1 || results[0].Status != "error" {
		t.Fatalf("history=%+v err=%v", results, err)
	}
}

func accountLeaseStep(until time.Time, token string) degradationSQLStep {
	return degradationSQLStep{contains: "FOR UPDATE", columns: []string{"id", "station_id", "account_external_id", "lease_token", "lease_until"}, rows: [][]driver.Value{{int64(7), int64(1), int64(11), token, until}}}
}

func TestAccountDegradationClaimBusy(t *testing.T) {
	steps := append(accountDegradationScopeSteps(), degradationSQLStep{contains: "ON CONFLICT", columns: []string{"id"}}, accountLeaseStep(time.Now().Add(time.Minute), "busy"))
	repo, state := newDegradationSQL(t, steps...)
	_, err := repo.ClaimAccountDegradation(context.Background(), 1, 11, time.Minute)
	if !errors.Is(err, ErrGroupDegradationBusy) || state.rollbacks != 1 {
		t.Fatalf("err=%v state=%+v", err, state)
	}
}

func TestAccountDegradationSaveFailureIsAtomic(t *testing.T) {
	for _, stale := range []bool{false, true} {
		token := "lease"
		if stale {
			token = "another"
		}
		steps := []degradationSQLStep{accountLeaseStep(time.Now().Add(time.Minute), token)}
		if !stale {
			steps = append(steps, degradationSQLStep{contains: `INSERT INTO "account_degradation_results"`, err: errors.New("insert failed")})
		}
		repo, state := newDegradationSQL(t, steps...)
		err := repo.CompleteAccountDegradation(context.Background(), AccountDegradationRun{ID: 7, StationID: 1, AccountExternalID: 11, LeaseToken: "lease"}, &AccountDegradationResult{})
		if err == nil || state.commits != 0 || state.rollbacks != 1 {
			t.Fatalf("stale=%v err=%v state=%+v", stale, err, state)
		}
	}
}

func TestAccountDegradationCompletionPersistsErrorAndPrunesOnlyAccount(t *testing.T) {
	steps := []degradationSQLStep{accountLeaseStep(time.Now().Add(time.Minute), "lease"),
		{contains: `INSERT INTO "account_degradation_results"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(55)}}},
		{contains: `DELETE FROM "account_degradation_results"`, check: func(query string, args []driver.NamedValue) {
			if strings.Count(query, "station_id =") != 2 || strings.Count(query, "account_external_id =") != 2 || args[len(args)-1].Value != int64(20) {
				t.Errorf("unscoped retention %s %+v", query, args)
			}
		}}, {contains: `UPDATE "account_degradation_runs"`},
	}
	repo, state := newDegradationSQL(t, steps...)
	result := AccountDegradationResult{GroupDegradationResult: GroupDegradationResult{Status: "error", Method: "candy", Reason: "timeout"}}
	err := repo.CompleteAccountDegradation(context.Background(), AccountDegradationRun{ID: 7, StationID: 1, AccountExternalID: 11, LeaseToken: "lease"}, &result)
	if err != nil || state.commits != 1 || result.ID != 55 || result.AccountExternalID != 11 || result.GroupExternalID != 0 || result.Status != "error" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAccountDegradationResultJSONIncludesSavedTimestamp(t *testing.T) {
	created := time.Now().UTC()
	result := AccountDegradationResult{GroupDegradationResult: GroupDegradationResult{ID: 1, Status: "normal"}, StationID: 1, CreatedAt: created}
	data, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(data), created.Format(time.RFC3339Nano)) || strings.Contains(string(data), "station_id") {
		t.Fatalf("result JSON=%s err=%v", data, err)
	}
}

func TestLatestAccountDegradationSingleResultQuery(t *testing.T) {
	steps := []degradationSQLStep{
		accountDegradationScopeSteps()[0],
		{contains: "SELECT DISTINCT ON (r.account_external_id) r.*", columns: []string{"id", "account_external_id", "status", "html", "answer"}, rows: [][]driver.Value{
			{int64(7), int64(11), "normal", "<html>preview</html>", ""},
			{int64(9), int64(12), "degraded", "", "candy answer"},
		}, check: func(query string, args []driver.NamedValue) {
			for _, clause := range []string{"JOIN relay_accounts", "a.relay_station_id = r.station_id", "a.external_id = r.account_external_id", "WHERE r.station_id = $1", "r.created_at DESC, r.id DESC"} {
				if !strings.Contains(query, clause) {
					t.Errorf("missing %s: %s", clause, query)
				}
			}
			if len(args) != 1 || args[0].Value != int64(1) {
				t.Errorf("unscoped args: %+v", args)
			}
		}},
	}
	repo, _ := newDegradationSQL(t, steps...)
	latest, err := repo.LatestAccountDegradation(context.Background(), 1)
	if err != nil || len(latest) != 2 || latest["11"].HTML != "<html>preview</html>" || latest["12"].Answer != "candy answer" {
		t.Fatalf("latest=%+v err=%v", latest, err)
	}
}
