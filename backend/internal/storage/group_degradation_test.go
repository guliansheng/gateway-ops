package storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func TestGroupDegradationDefaultsAndValidation(t *testing.T) {
	c := DefaultGroupDegradationConfig()
	if c.Enabled || c.Method != "pelican" || c.Model != "" || c.JudgeModel != "" || c.Effort != "none" || c.IntervalMinutes != 15 || c.PreviewMode != "hover" || c.ResultVisibility != "show" || c.HistoryLimit != 20 {
		t.Fatalf("defaults = %+v", c)
	}
	for _, mutate := range []func(*GroupDegradationConfig){
		func(c *GroupDegradationConfig) { c.Method = "other" },
		func(c *GroupDegradationConfig) { c.Effort = "extreme" },
		func(c *GroupDegradationConfig) { c.PreviewMode = "other" },
		func(c *GroupDegradationConfig) { c.ResultVisibility = "other" },
		func(c *GroupDegradationConfig) { c.IntervalMinutes = 4 },
		func(c *GroupDegradationConfig) { c.IntervalMinutes = 0 },
		func(c *GroupDegradationConfig) { c.IntervalMinutes = 1441 },
		func(c *GroupDegradationConfig) { c.Enabled = true },
		func(c *GroupDegradationConfig) { c.Model = "bad\nmodel" },
		func(c *GroupDegradationConfig) { c.JudgeModel = strings.Repeat("a", 257) },
	} {
		c := DefaultGroupDegradationConfig()
		mutate(&c)
		if _, err := NormalizeGroupDegradationConfig(c); !errors.Is(err, ErrInvalidGroupDegradationConfig) {
			t.Fatalf("accepted %+v: %v", c, err)
		}
	}
	for _, effort := range []string{"none", "low", "medium", "high"} {
		c := DefaultGroupDegradationConfig()
		c.Enabled, c.Model, c.Method, c.Effort, c.PreviewMode = true, " model ", "candy", effort, "click"
		if got, err := NormalizeGroupDegradationConfig(c); err != nil || got.Model != "model" || got.JudgeModel != "" {
			t.Fatalf("normalized = %+v err=%v", got, err)
		}
	}
	c = DefaultGroupDegradationConfig()
	c.Method = "both"
	c.Enabled = true
	c.Model = "model"
	if got, err := NormalizeGroupDegradationConfig(c); err != nil || got.Method != "both" {
		t.Fatalf("both method=%+v err=%v", got, err)
	}
}

func TestGroupDegradationJSONContractAndIndependentSchema(t *testing.T) {
	config := DefaultGroupDegradationConfig()
	config.LeaseToken = "private-lease"
	data, _ := json.Marshal(GroupDegradationView{Config: config, Results: []GroupDegradationResult{}})
	var view map[string]json.RawMessage
	_ = json.Unmarshal(data, &view)
	if string(view["results"]) != "[]" || len(view) != 2 {
		t.Fatalf("view = %s", data)
	}
	var fields map[string]any
	_ = json.Unmarshal(view["config"], &fields)
	if len(fields) != 11 || fields["result_visibility"] != "show" || fields["history_limit"] != float64(20) || strings.Contains(string(data), "lease") || strings.Contains(string(data), "station_id") {
		t.Fatalf("config = %s", data)
	}
	data, _ = json.Marshal(GroupDegradationResult{HTML: "<html></html>", Answer: "saved answer"})
	var resultFields map[string]any
	_ = json.Unmarshal(data, &resultFields)
	if len(resultFields) != 10 {
		t.Fatalf("result fields = %s", data)
	}
	for _, model := range []any{&GroupDegradationConfig{}, &GroupDegradationResult{}} {
		s, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		if len(s.Relationships.Relations) != 0 || s.LookUpField("StationID") == nil || s.LookUpField("GroupExternalID") == nil {
			t.Fatal("snapshot-dependent schema")
		}
	}
}

// The queued SQL driver exercises GORM transactions and scopes without opening
// PostgreSQL, running AutoMigrate, or contacting a model provider.
type degradationSQLStep struct {
	contains string
	columns  []string
	rows     [][]driver.Value
	err      error
	check    func(string, []driver.NamedValue)
}
type degradationSQL struct {
	t                  *testing.T
	steps              []degradationSQLStep
	commits, rollbacks int
}
type degradationConnector struct{ state *degradationSQL }
type degradationDriver struct{}
type degradationConn struct{ state *degradationSQL }
type degradationTx struct{ state *degradationSQL }
type degradationRows struct {
	columns []string
	rows    [][]driver.Value
}

func (c degradationConnector) Connect(context.Context) (driver.Conn, error) {
	return &degradationConn{state: c.state}, nil
}
func (c degradationConnector) Driver() driver.Driver       { return degradationDriver{} }
func (degradationDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }
func (c *degradationConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *degradationConn) Close() error              { return nil }
func (c *degradationConn) Begin() (driver.Tx, error) { return &degradationTx{state: c.state}, nil }
func (c *degradationConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (tx *degradationTx) Commit() error   { tx.state.commits++; return nil }
func (tx *degradationTx) Rollback() error { tx.state.rollbacks++; return nil }
func (c *degradationConn) step(query string, args []driver.NamedValue) degradationSQLStep {
	c.state.t.Helper()
	if len(c.state.steps) == 0 {
		c.state.t.Fatalf("unexpected SQL: %s", query)
	}
	step := c.state.steps[0]
	c.state.steps = c.state.steps[1:]
	if !strings.Contains(query, step.contains) {
		c.state.t.Fatalf("SQL %s does not contain %q", query, step.contains)
	}
	if step.check != nil {
		step.check(query, args)
	}
	return step
}
func (c *degradationConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step := c.step(query, args)
	if step.err != nil {
		return nil, step.err
	}
	return &degradationRows{columns: step.columns, rows: step.rows}, nil
}
func (c *degradationConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step := c.step(query, args)
	return driver.RowsAffected(1), step.err
}
func (r *degradationRows) Columns() []string { return r.columns }
func (r *degradationRows) Close() error      { return nil }
func (r *degradationRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}
func newDegradationSQL(t *testing.T, steps ...degradationSQLStep) (*RelayStations, *degradationSQL) {
	t.Helper()
	state := &degradationSQL{t: t, steps: steps}
	sqlDB := sql.OpenDB(degradationConnector{state: state})
	t.Cleanup(func() {
		_ = sqlDB.Close()
		if len(state.steps) > 0 {
			t.Errorf("%d SQL steps not consumed", len(state.steps))
		}
	})
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return NewRelayStations(db), state
}
func degradationScopeSteps(monitor bool) []degradationSQLStep {
	return []degradationSQLStep{
		{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
		{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id", "monitor_enabled"}, rows: [][]driver.Value{{int64(1), int64(9), monitor}}},
	}
}
func degradationConfigStep(enabled bool, leaseUntil driver.Value) degradationSQLStep {
	return degradationSQLStep{contains: `FROM "group_degradation_configs"`, columns: []string{"id", "station_id", "group_external_id", "enabled", "method", "model", "judge_model", "effort", "interval_minutes", "preview_mode", "result_visibility", "lease_token", "lease_until", "history_limit"}, rows: [][]driver.Value{{int64(7), int64(1), int64(9), enabled, "candy", "candidate", "", "medium", int64(15), "hover", "show", "lease-token", leaseUntil, int64(100)}}}
}

func TestGroupDegradationPublicReadGatesAndNoCreation(t *testing.T) {
	for _, tc := range []struct {
		name             string
		monitor, enabled bool
		want             int
	}{
		{"monitor disabled", false, true, 0}, {"config disabled", true, false, 0}, {"both enabled", true, true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append(degradationScopeSteps(tc.monitor), degradationConfigStep(tc.enabled, nil))
			if tc.want > 0 {
				// 双桶视图：成功桶含 normal 记录，失败桶为空。
				steps = append(steps,
					degradationSQLStep{contains: `FROM "group_degradation_results"`, columns: []string{"id", "status"}, rows: [][]driver.Value{{int64(1), "normal"}}},
					degradationSQLStep{contains: `FROM "group_degradation_results"`, columns: []string{"id", "status"}},
				)
			}
			repo, _ := newDegradationSQL(t, steps...)
			view, err := repo.GroupDegradationView(context.Background(), 1, 9, true)
			if err != nil || len(view.Results) != tc.want || view.Config.Enabled != (tc.monitor && tc.enabled) {
				t.Fatalf("view=%+v err=%v", view, err)
			}
		})
	}
	steps := append(degradationScopeSteps(true), degradationSQLStep{contains: `FROM "group_degradation_configs"`, columns: []string{"id"}})
	repo, _ := newDegradationSQL(t, steps...)
	view, err := repo.GroupDegradationView(context.Background(), 1, 9, false)
	if err != nil || !reflect.DeepEqual(view.Config, DefaultGroupDegradationConfig()) || len(view.Results) != 0 {
		t.Fatalf("default view=%+v err=%v", view, err)
	}
}

func TestGroupDegradationClaimDisabledAndActiveLease(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		lease   driver.Value
		want    error
	}{
		{false, nil, ErrGroupDegradationDisabled}, {true, time.Now().Add(time.Minute), ErrGroupDegradationBusy},
	} {
		steps := append(degradationScopeSteps(true), degradationConfigStep(tc.enabled, tc.lease))
		steps[len(steps)-1].contains = "FOR UPDATE"
		repo, state := newDegradationSQL(t, steps...)
		_, err := repo.ClaimGroupDegradation(context.Background(), 1, 9, 5*time.Minute)
		if !errors.Is(err, tc.want) || state.rollbacks != 1 || state.commits != 0 {
			t.Fatalf("claim err=%v commits=%d rollbacks=%d", err, state.commits, state.rollbacks)
		}
	}
}

func TestGroupDegradationManualClaimKeepsPendingSchedule(t *testing.T) {
	pending := time.Now().Add(56 * time.Minute).Truncate(time.Microsecond)
	step := degradationSQLStep{
		columns: []string{"id", "station_id", "group_external_id", "enabled", "method", "model", "judge_model", "effort", "interval_minutes", "preview_mode", "result_visibility", "lease_token", "lease_until", "history_limit", "pelican_interval_minutes", "candy_interval_minutes", "pelican_next_run_at", "candy_next_run_at"},
		rows:    [][]driver.Value{{int64(7), int64(1), int64(9), true, "pelican", "candidate", "", "medium", int64(60), "hover", "show", "", nil, int64(20), int64(60), int64(30), pending, nil}},
	}
	step.contains = "FOR UPDATE"
	update := degradationSQLStep{contains: `UPDATE "group_degradation_configs"`, check: func(query string, args []driver.NamedValue) {
		kept := 0
		for _, arg := range args {
			value, ok := arg.Value.(time.Time)
			if !ok {
				continue
			}
			if value.Equal(pending.Add(time.Hour)) {
				t.Errorf("manual claim pushed the pending slot to %v", value)
			}
			if value.Equal(pending) {
				kept++
			}
		}
		if kept != 2 {
			t.Errorf("pending slot written %d times, want next_run_at and pelican_next_run_at", kept)
		}
	}}
	steps := append(degradationScopeSteps(true), step, update)
	repo, state := newDegradationSQL(t, steps...)
	config, err := repo.ClaimGroupDegradation(context.Background(), 1, 9, 5*time.Minute)
	if err != nil || state.commits != 1 || config.NextRunAt == nil || !config.NextRunAt.Equal(pending) {
		t.Fatalf("claim err=%v commits=%d next=%v", err, state.commits, config.NextRunAt)
	}
}

func TestGroupDegradationDueClaimUsesSkipLockedAndLease(t *testing.T) {
	step := degradationConfigStep(true, nil)
	step.contains = "FOR UPDATE SKIP LOCKED"
	step.check = func(query string, args []driver.NamedValue) {
		if len(args) != 4 || args[3].Value != int64(2) {
			t.Errorf("due limit arguments: %+v", args)
		}
		for _, fragment := range []string{"enabled =", "next_run_at <=", "lease_until <=", "relay_stations", "deleted_at IS NULL", "relay_groups", "LIMIT $4"} {
			if !strings.Contains(query, fragment) {
				t.Errorf("due SQL missing %q: %s", fragment, query)
			}
		}
	}
	repo, state := newDegradationSQL(t, step, degradationSQLStep{contains: `UPDATE "group_degradation_configs"`})
	configs, err := repo.ClaimDueGroupDegradations(context.Background(), 2, 5*time.Minute)
	if err != nil || len(configs) != 1 || len(configs[0].LeaseToken) != 48 || configs[0].LeaseUntil == nil || configs[0].NextRunAt == nil || state.commits != 1 {
		t.Fatalf("configs=%+v err=%v", configs, err)
	}
}

func TestGroupDegradationCompletionIsAtomicAndErrorsPersist(t *testing.T) {
	for _, failInsert := range []bool{false, true} {
		step := degradationConfigStep(true, time.Now().Add(time.Minute))
		step.contains = "FOR UPDATE"
		insert := degradationSQLStep{contains: `INSERT INTO "group_degradation_results"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(55)}}}
		if failInsert {
			insert.err = errors.New("mock insert failure")
		}
		steps := []degradationSQLStep{step, insert}
		if !failInsert {
			steps = append(steps, degradationPruneStep(t, 1, 9, 100), degradationSQLStep{contains: `UPDATE "group_degradation_configs"`})
		}
		repo, state := newDegradationSQL(t, steps...)
		result := GroupDegradationResult{Method: "candy", Model: "candidate", JudgeModel: "candidate", Effort: "medium", Status: "error", Reason: "请求超时"}
		err := repo.CompleteGroupDegradation(context.Background(), GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, LeaseToken: "lease-token"}, &result)
		if failInsert {
			if err == nil || state.commits != 0 || state.rollbacks != 1 {
				t.Fatalf("failed insert advanced state: err=%v", err)
			}
		} else if err != nil || result.ID != 55 || result.CreatedAt.IsZero() || state.commits != 1 || result.Status != "error" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestGroupDegradationLostLeaseDoesNotSave(t *testing.T) {
	step := degradationConfigStep(true, time.Now().Add(time.Minute))
	step.contains = "FOR UPDATE"
	repo, state := newDegradationSQL(t, step)
	err := repo.CompleteGroupDegradation(context.Background(), GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, LeaseToken: "stale-token"}, &GroupDegradationResult{})
	if !errors.Is(err, ErrGroupDegradationLeaseLost) || state.rollbacks != 1 {
		t.Fatalf("error=%v", err)
	}
}

// degradationUpdateArg returns the value bound to one column of a GORM map
// update, so assertions do not depend on argument order or on updated_at.
func degradationUpdateArg(args []driver.NamedValue, query, column string) (driver.Value, bool) {
	match := regexp.MustCompile(`"` + regexp.QuoteMeta(column) + `"\s*=\s*\$(\d+)`).FindStringSubmatch(query)
	if match == nil {
		return nil, false
	}
	index, err := strconv.Atoi(match[1])
	if err != nil || index < 1 || index > len(args) {
		return nil, false
	}
	return args[index-1].Value, true
}

func TestGroupDegradationSaveFirstEnableAndInterval(t *testing.T) {
	for _, previouslyEnabled := range []bool{false, true} {
		t.Run(fmt.Sprint(previouslyEnabled), func(t *testing.T) {
			lastRun := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
			current := degradationConfigStep(previouslyEnabled, nil)
			current.contains = "FOR UPDATE"
			current.columns = append(current.columns, "last_run_at")
			current.rows[0] = append(current.rows[0], lastRun)
			update := degradationSQLStep{contains: `UPDATE "group_degradation_configs"`, check: func(query string, args []driver.NamedValue) {
				if !strings.Contains(query, `"next_run_at"`) || !strings.Contains(query, `"enabled"`) || !strings.Contains(query, `"result_visibility"`) {
					t.Errorf("incomplete config update: %s", query)
				}
				wantNext := lastRun.Add(30 * time.Minute)
				next, ok := degradationUpdateArg(args, query, "next_run_at")
				if !ok {
					t.Fatalf("no next_run_at in update: %s", query)
				}
				nextAt, isTime := next.(time.Time)
				if !isTime {
					t.Fatalf("next_run_at is %T, want time", next)
				}
				if previouslyEnabled {
					if !nextAt.Equal(wantNext) {
						t.Errorf("interval change next=%v want=%v", nextAt, wantNext)
					}
				} else if nextAt.Before(time.Now().Add(-time.Minute)) || nextAt.After(time.Now().Add(time.Minute)) {
					// A first enable is scheduled right away instead of one interval later.
					t.Errorf("first enable next=%v, want now", nextAt)
				}
			}}
			steps := append(degradationScopeSteps(true), degradationSQLStep{contains: "ON CONFLICT", columns: []string{"id"}, rows: [][]driver.Value{{int64(7)}}}, current, update, degradationPruneStep(t, 1, 9, 20))
			repo, state := newDegradationSQL(t, steps...)
			config := DefaultGroupDegradationConfig()
			config.Enabled, config.Model, config.IntervalMinutes = true, "candidate", 30
			config.ResultVisibility = "hide"
			if err := repo.SaveGroupDegradationConfig(context.Background(), 1, 9, config); err != nil || state.commits != 1 {
				t.Fatalf("save err=%v commits=%d", err, state.commits)
			}
		})
	}
}

func TestGroupDegradationSaveRejectsActiveLease(t *testing.T) {
	current := degradationConfigStep(true, time.Now().Add(time.Minute))
	current.contains = "FOR UPDATE"
	steps := append(degradationScopeSteps(true), degradationSQLStep{contains: "ON CONFLICT", columns: []string{"id"}}, current)
	repo, state := newDegradationSQL(t, steps...)
	err := repo.SaveGroupDegradationConfig(context.Background(), 1, 9, DefaultGroupDegradationConfig())
	if !errors.Is(err, ErrGroupDegradationBusy) || state.rollbacks != 1 {
		t.Fatalf("save active lease err=%v", err)
	}
}

func TestGroupDegradationExpiredLeaseDoesNotSave(t *testing.T) {
	step := degradationConfigStep(true, time.Now().Add(-time.Second))
	step.contains = "FOR UPDATE"
	repo, _ := newDegradationSQL(t, step)
	err := repo.CompleteGroupDegradation(context.Background(), GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, LeaseToken: "lease-token"}, &GroupDegradationResult{})
	if !errors.Is(err, ErrGroupDegradationLeaseLost) {
		t.Fatalf("expired lease err=%v", err)
	}
}

func TestGroupDegradationResultVisibility(t *testing.T) {
	for _, tc := range []struct {
		name, visibility string
		public           bool
	}{
		{"public hidden", "hide", true},
		{"public shown", "show", true},
		{"admin hidden", "hide", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := degradationConfigStep(true, nil)
			config.rows[0][10] = tc.visibility
			hidden := tc.public && tc.visibility == "hide"
			successStep := degradationSQLStep{
				contains: `FROM "group_degradation_results"`,
				columns:  []string{"id", "status", "reason", "html", "answer"},
				rows:     [][]driver.Value{{int64(1), "normal", "private review", "private HTML", "private answer"}},
				check: func(query string, _ []driver.NamedValue) {
					if hidden {
						for _, field := range []string{`SELECT *`, `"html"`, `"answer"`, `"reason"`} {
							if strings.Contains(query, field) {
								t.Errorf("hidden view requested %s: %s", field, query)
							}
						}
					}
				},
			}
			failedStep := degradationSQLStep{contains: `FROM "group_degradation_results"`}
			if hidden {
				newest := time.Now().Truncate(time.Microsecond)
				// created_at 递减排列，让双桶合并排序（created_at DESC, id DESC）
				// 还原旧单查询返回的 [1,2,3,4] 顺序。
				successStep.columns = []string{"id", "status", "created_at"}
				successStep.rows = [][]driver.Value{{int64(1), "normal", newest}, {int64(2), "degraded", newest.Add(-10 * time.Minute)}, {int64(4), "inconclusive", newest.Add(-30 * time.Minute)}}
				failedStep.columns = []string{"id", "status", "created_at"}
				failedStep.rows = [][]driver.Value{{int64(3), "error", newest.Add(-20 * time.Minute)}}
			}
			steps := append(degradationScopeSteps(true), config, successStep, failedStep)
			repo, _ := newDegradationSQL(t, steps...)
			view, err := repo.GroupDegradationView(context.Background(), 1, 9, tc.public)
			if err != nil || len(view.Results) == 0 {
				t.Fatalf("view=%+v err=%v", view, err)
			}
			if hidden {
				want := []string{"通过检测", "未通过检测，疑似降智", "检测失败，本次不判定是否通过", "结果待确认，暂无法判定是否通过"}
				for i, result := range view.Results {
					if result.Reason != want[i] || result.HTML != "" || result.Answer != "" {
						t.Errorf("hidden result=%+v", result)
					}
				}
				data, _ := json.Marshal(view)
				for _, field := range []string{`"html"`, `"answer"`, "private"} {
					if strings.Contains(string(data), field) {
						t.Errorf("hidden JSON leaked %s: %s", field, data)
					}
				}
			} else if result := view.Results[0]; result.Reason != "private review" || result.HTML != "private HTML" || result.Answer != "private answer" {
				t.Fatalf("full result lost: %+v", result)
			}
		})
	}
}

func TestGroupDegradationResultVisibilityDefaultsForOlderClients(t *testing.T) {
	config := DefaultGroupDegradationConfig()
	config.ResultVisibility = ""
	got, err := NormalizeGroupDegradationConfig(config)
	if err != nil || got.ResultVisibility != "show" {
		t.Fatalf("config=%+v err=%v", got, err)
	}
	config.ResultVisibility = " hide "
	got, err = NormalizeGroupDegradationConfig(config)
	if err != nil || got.ResultVisibility != "hide" {
		t.Fatalf("config=%+v err=%v", got, err)
	}
}

func degradationPruneStep(t *testing.T, stationID uint, groupID int64, limit int) degradationSQLStep {
	t.Helper()
	return degradationSQLStep{contains: `DELETE FROM "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
		// 成功与失败两个桶各自按上限截断：失败记录不占成功记录的名额。
		if strings.Count(query, `id IN (SELECT "id" FROM "group_degradation_results"`) != 2 || !strings.Contains(query, "status <> $") || !strings.Contains(query, "status = $") || strings.Count(query, "ORDER BY created_at DESC,id DESC") != 2 || strings.Count(query, "OFFSET $") != 2 {
			t.Errorf("prune buckets missing: %s", query)
		}
		// 两个状态桶互斥，过期记录必须取并集；AND 会导致一条也删不掉。
		if !regexp.MustCompile(`(?s)AND \(+id IN \(SELECT .* OR id IN \(SELECT .*\)\)+`).MatchString(query) {
			t.Errorf("prune must use a parenthesized union of stale buckets: %s", query)
		}
		if !strings.Contains(query, "station_id = $1 AND group_external_id = $2") {
			t.Errorf("prune lost outer tenant scope: %s", query)
		}
		want := []driver.Value{int64(stationID), groupID, int64(stationID), groupID, "error", int64(limit), int64(stationID), groupID, "error", int64(limit)}
		if len(args) != len(want) {
			t.Fatalf("prune args=%+v", args)
		}
		for i := range want {
			if args[i].Value != want[i] {
				t.Errorf("prune arg %d=%v want=%v", i, args[i].Value, want[i])
			}
		}
	}}
}

func TestGroupDegradationHistoryLimitValidationAndLegacyDefault(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{0, 20}, {1, 1}, {18, 18}, {100, 100}, {1000, 1000}} {
		config := DefaultGroupDegradationConfig()
		config.HistoryLimit = tc.input
		got, err := NormalizeGroupDegradationConfig(config)
		if err != nil || got.HistoryLimit != tc.want {
			t.Fatalf("limit=%d config=%+v err=%v", tc.input, got, err)
		}
	}
	for _, limit := range []int{-1, 1001} {
		config := DefaultGroupDegradationConfig()
		config.HistoryLimit = limit
		if _, err := NormalizeGroupDegradationConfig(config); !errors.Is(err, ErrInvalidGroupDegradationConfig) {
			t.Fatalf("accepted limit %d: %v", limit, err)
		}
	}
}

func TestGroupDegradationViewHistoryLimitsAndScope(t *testing.T) {
	for _, tc := range []struct {
		limit, want int
		public      bool
	}{
		{0, 20, false}, {1, 1, false}, {37, 37, false}, {1000, 1000, false},
		{0, 18, true}, {1, 1, true}, {17, 17, true}, {1000, 18, true},
	} {
		t.Run(fmt.Sprintf("%d/public=%v", tc.limit, tc.public), func(t *testing.T) {
			config := degradationConfigStep(true, nil)
			config.rows[0][13] = int64(tc.limit)
			results := func(failed bool) degradationSQLStep {
				operator := "status <> $3"
				if failed {
					operator = "status = $3"
				}
				return degradationSQLStep{contains: `FROM "group_degradation_results"`, columns: []string{"id"}, check: func(query string, args []driver.NamedValue) {
					if !strings.Contains(query, "station_id = $1 AND group_external_id = $2") || !strings.Contains(query, operator) || !strings.Contains(query, "ORDER BY created_at DESC,id DESC LIMIT $4") || len(args) != 4 || args[0].Value != int64(1) || args[1].Value != int64(9) || args[2].Value != "error" || args[3].Value != int64(tc.want) {
						t.Errorf("view scope/limit SQL=%s args=%+v", query, args)
					}
				}}
			}
			steps := append(degradationScopeSteps(true), config, results(false), results(true))
			repo, _ := newDegradationSQL(t, steps...)
			view, err := repo.GroupDegradationView(context.Background(), 1, 9, tc.public)
			if err != nil || view.Config.HistoryLimit != groupDegradationHistoryLimit(tc.limit) {
				t.Fatalf("view=%+v err=%v", view, err)
			}
		})
	}
}

// 成功与失败两个桶各按上限读取，合并顺序必须与单个 SQL 的
// created_at DESC, id DESC 一致，否则前端“最新一条”会随桶切换而跳动。
func TestGroupDegradationViewMergesBothVerdictBuckets(t *testing.T) {
	config := degradationConfigStep(true, nil)
	older, middle, newer :=
		time.Now().Add(-3*time.Hour).Truncate(time.Microsecond),
		time.Now().Add(-2*time.Hour).Truncate(time.Microsecond),
		time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	columns := []string{"id", "status", "created_at"}
	steps := append(degradationScopeSteps(true), config,
		degradationSQLStep{contains: `FROM "group_degradation_results"`, columns: columns, rows: [][]driver.Value{{int64(4), "normal", newer}, {int64(2), "normal", older}}},
		degradationSQLStep{contains: `FROM "group_degradation_results"`, columns: columns, rows: [][]driver.Value{{int64(3), "error", middle}}})
	repo, _ := newDegradationSQL(t, steps...)
	view, err := repo.GroupDegradationView(context.Background(), 1, 9, false)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]uint, 0, len(view.Results))
	for _, result := range view.Results {
		ids = append(ids, result.ID)
	}
	if !reflect.DeepEqual(ids, []uint{4, 3, 2}) {
		t.Fatalf("merged order = %v", ids)
	}
}

func TestGroupDegradationSaveHistoryLimitAndPruneRollback(t *testing.T) {
	for _, pruneFails := range []bool{false, true} {
		t.Run(fmt.Sprint(pruneFails), func(t *testing.T) {
			configStep := degradationConfigStep(true, nil)
			configStep.contains = "FOR UPDATE"
			prune := degradationPruneStep(t, 1, 9, 3)
			if pruneFails {
				prune.err = errors.New("prune failed")
			}
			update := degradationSQLStep{contains: `UPDATE "group_degradation_configs"`, check: func(query string, args []driver.NamedValue) {
				if !strings.Contains(query, `"history_limit"=`) || args[4].Value != int64(3) {
					t.Errorf("missing history limit update: %s %+v", query, args)
				}
				if !strings.Contains(query, `WHERE "id" =`) || args[len(args)-1].Value != int64(7) {
					t.Errorf("wrong config update scope: %s %+v", query, args)
				}
			}}
			steps := append(degradationScopeSteps(true), degradationSQLStep{contains: "ON CONFLICT", columns: []string{"id"}}, configStep, update, prune)
			repo, state := newDegradationSQL(t, steps...)
			config := DefaultGroupDegradationConfig()
			config.HistoryLimit, config.ID, config.StationID, config.GroupExternalID = 3, 999, 888, 777
			err := repo.SaveGroupDegradationConfig(context.Background(), 1, 9, config)
			if pruneFails {
				if err == nil || state.rollbacks != 1 || state.commits != 0 {
					t.Fatalf("failed prune committed: err=%v state=%+v", err, state)
				}
			} else if err != nil || state.commits != 1 {
				t.Fatalf("save err=%v state=%+v", err, state)
			}
		})
	}
}

func TestGroupDegradationCompletePrunesCurrentNotClaimedLimit(t *testing.T) {
	for _, limit := range []int{0, 1, 3, 1000} {
		for _, pruneFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("limit=%d/fail=%v", limit, pruneFails), func(t *testing.T) {
				current := degradationConfigStep(true, time.Now().Add(time.Minute))
				current.contains = "FOR UPDATE"
				current.rows[0][13] = int64(limit)
				current.check = func(query string, args []driver.NamedValue) {
					if !strings.Contains(query, "station_id = $1 AND group_external_id = $2") || len(args) != 4 || args[0].Value != int64(1) || args[1].Value != int64(9) || args[2].Value != int64(7) {
						t.Errorf("completion row lock not scoped: %s %+v", query, args)
					}
				}
				prune := degradationPruneStep(t, 1, 9, groupDegradationHistoryLimit(limit))
				if pruneFails {
					prune.err = errors.New("prune failed")
				}
				steps := []degradationSQLStep{current, {contains: `INSERT INTO "group_degradation_results"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(55)}}}, prune}
				if !pruneFails {
					steps = append(steps, degradationSQLStep{contains: `UPDATE "group_degradation_configs"`})
				}
				repo, state := newDegradationSQL(t, steps...)
				claimed := GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, LeaseToken: "lease-token", HistoryLimit: 999}
				result := GroupDegradationResult{StationID: 888, GroupExternalID: 777, Status: "error"}
				err := repo.CompleteGroupDegradation(context.Background(), claimed, &result)
				if pruneFails {
					if err == nil || state.rollbacks != 1 || state.commits != 0 {
						t.Fatalf("failed prune committed: %v", err)
					}
				} else if err != nil || state.commits != 1 || result.StationID != 1 || result.GroupExternalID != 9 {
					t.Fatalf("result=%+v err=%v", result, err)
				}
			})
		}
	}
}

func TestGroupDegradationCompleteRejectsWrongConfigScope(t *testing.T) {
	for _, mutate := range []func(*degradationSQLStep){
		func(s *degradationSQLStep) { s.rows[0][0] = int64(8) },
		func(s *degradationSQLStep) { s.rows[0][1] = int64(2) },
		func(s *degradationSQLStep) { s.rows[0][2] = int64(10) },
		func(s *degradationSQLStep) { s.rows = nil },
	} {
		current := degradationConfigStep(true, time.Now().Add(time.Minute))
		current.contains = "FOR UPDATE"
		mutate(&current)
		repo, state := newDegradationSQL(t, current)
		err := repo.CompleteGroupDegradation(context.Background(), GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, LeaseToken: "lease-token"}, &GroupDegradationResult{})
		if !errors.Is(err, ErrGroupDegradationLeaseLost) || state.rollbacks != 1 || state.commits != 0 {
			t.Fatalf("wrong scope completed: err=%v", err)
		}
	}
}

func TestGroupDegradationClearHistoryLocksAndPreservesSchedule(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		lease                driver.Value
		missing, deleteFails bool
	}{
		{name: "clear"}, {name: "expired lease", lease: time.Now().Add(-time.Minute)},
		{name: "active lease", lease: time.Now().Add(time.Minute)}, {name: "no config", missing: true},
		{name: "delete failure", deleteFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			last, next := time.Now().Add(-time.Hour).Truncate(time.Microsecond), time.Now().Add(time.Hour).Truncate(time.Microsecond)
			current := degradationConfigStep(true, tc.lease)
			current.contains = "FOR UPDATE"
			current.columns = append(current.columns, "last_run_at", "next_run_at")
			current.rows[0] = append(current.rows[0], last, next)
			current.rows[0][13] = int64(0)
			if tc.missing {
				current.rows = nil
			}
			steps := append(degradationScopeSteps(true), current)
			active := tc.name == "active lease"
			if !active && !tc.missing {
				deleteStep := degradationSQLStep{contains: `DELETE FROM "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
					if !strings.Contains(query, "station_id = $1 AND group_external_id = $2") || len(args) != 2 || args[0].Value != int64(1) || args[1].Value != int64(9) {
						t.Errorf("unscoped clear: %s %+v", query, args)
					}
				}}
				if tc.deleteFails {
					deleteStep.err = errors.New("delete failed")
				}
				steps = append(steps, deleteStep)
			}
			repo, state := newDegradationSQL(t, steps...)
			view, err := repo.ClearGroupDegradationHistory(context.Background(), 1, 9, "all")
			if active {
				if !errors.Is(err, ErrGroupDegradationBusy) || state.rollbacks != 1 {
					t.Fatalf("cleared active lease: %v", err)
				}
			} else if tc.deleteFails {
				if err == nil || state.rollbacks != 1 || state.commits != 0 {
					t.Fatalf("delete failure committed: %v", err)
				}
			} else {
				if err != nil || state.commits != 1 || view.Results == nil || len(view.Results) != 0 || view.Config.HistoryLimit != 20 {
					t.Fatalf("view=%+v err=%v", view, err)
				}
				if tc.missing {
					if !reflect.DeepEqual(view.Config, DefaultGroupDegradationConfig()) {
						t.Fatalf("missing config created: %+v", view.Config)
					}
				} else if view.Config.LastRunAt == nil || !view.Config.LastRunAt.Equal(last) || view.Config.NextRunAt == nil || !view.Config.NextRunAt.Equal(next) || !view.Config.Enabled {
					t.Fatalf("clear changed schedule/config: %+v", view.Config)
				}
			}
		})
	}
}

func TestGroupDegradationClearHistoryValidatesStationAndGroup(t *testing.T) {
	for _, missingStation := range []bool{true, false} {
		steps := degradationScopeSteps(true)
		if missingStation {
			steps = steps[:1]
			steps[0].rows = nil
		} else {
			steps[1].rows = nil
			steps[1].check = func(query string, args []driver.NamedValue) {
				if !strings.Contains(query, "relay_station_id = $1 AND external_id = $2") || args[0].Value != int64(1) || args[1].Value != int64(9) {
					t.Errorf("group scope SQL=%s %+v", query, args)
				}
			}
		}
		repo, state := newDegradationSQL(t, steps...)
		_, err := repo.ClearGroupDegradationHistory(context.Background(), 1, 9, "all")
		if !errors.Is(err, gorm.ErrRecordNotFound) || state.commits != 0 || state.rollbacks != 0 {
			t.Fatalf("clear invalid scope err=%v", err)
		}
	}
}

// 清空历史按当前筛选收窄：错误 / 成功 / 单一测试方式各删自己那份，未知筛选
// 直接拒绝而不落库，避免手写错参数后误删整段历史。
func TestGroupDegradationClearHistoryHonorsFilter(t *testing.T) {
	for _, tc := range []struct {
		filter string
		scope  string
		values []any
	}{
		{filter: "all"},
		{filter: "", scope: "", values: nil},
		{filter: "error", scope: "status =", values: []any{"error"}},
		{filter: "success", scope: "status <>", values: []any{"error"}},
		{filter: "pelican", scope: "method =", values: []any{"pelican"}},
		{filter: "candy", scope: "method =", values: []any{"candy"}},
	} {
		name := tc.filter
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			config := degradationConfigStep(true, nil)
			config.contains = "FOR UPDATE"
			clear := degradationSQLStep{contains: `DELETE FROM "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
				if !strings.Contains(query, "station_id = $") || !strings.Contains(query, "group_external_id = $") {
					t.Errorf("clear lost scope: %s", query)
				}
				if tc.scope == "" {
					if strings.Contains(query, "status") || strings.Contains(query, "method") {
						t.Errorf("clear narrowed an unfiltered delete: %s", query)
					}
				} else if !strings.Contains(query, tc.scope) {
					t.Errorf("clear missing %q: %s", tc.scope, query)
				}
				if len(args) != 2+len(tc.values) {
					t.Fatalf("clear args=%+v", args)
				}
				for _, want := range append([]any{int64(1), int64(9)}, tc.values...) {
					found := false
					for _, arg := range args {
						if arg.Value == want {
							found = true
						}
					}
					if !found {
						t.Errorf("clear args missing %v: %+v", want, args)
					}
				}
			}}
			repo, state := newDegradationSQL(t, append(degradationScopeSteps(true), config, clear)...)
			if _, err := repo.ClearGroupDegradationHistory(context.Background(), 1, 9, tc.filter); err != nil || state.commits != 1 || state.rollbacks != 0 {
				t.Fatalf("clear %q err=%v commits=%d rollbacks=%d", tc.filter, err, state.commits, state.rollbacks)
			}
		})
	}
	t.Run("unknown filter", func(t *testing.T) {
		config := degradationConfigStep(true, nil)
		config.contains = "FOR UPDATE"
		repo, state := newDegradationSQL(t, append(degradationScopeSteps(true), config)...)
		if _, err := repo.ClearGroupDegradationHistory(context.Background(), 1, 9, "errr"); !errors.Is(err, ErrInvalidDegradationFilter) || state.commits != 0 || state.rollbacks != 1 {
			t.Fatalf("unknown filter err=%v commits=%d rollbacks=%d", err, state.commits, state.rollbacks)
		}
	})
}

func TestGroupDegradationHistoryMigrationPreservesPositiveChoices(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			count := int64(0)
			if exists {
				count = 1
			}
			steps := []degradationSQLStep{{contains: "column_name", columns: []string{"count"}, rows: [][]driver.Value{{count}}}}
			if exists {
				steps = append(steps, degradationSQLStep{contains: "UPDATE group_degradation_configs SET history_limit = 20 WHERE history_limit = 0 OR history_limit IS NULL", check: func(query string, _ []driver.NamedValue) {
					if strings.Contains(query, "history_limit >") || strings.Contains(query, "history_limit >=") || strings.Contains(query, "degradation_prompt_settings") {
						t.Errorf("migration overwrites user choices/prompts: %s", query)
					}
				}})
			}
			repo, _ := newDegradationSQL(t, steps...)
			if err := migrateGroupDegradationHistory(repo.db); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDeleteGroupDegradationResultScopesToStationAndGroup(t *testing.T) {
	steps := degradationScopeSteps(false)
	steps = append(steps, degradationSQLStep{contains: `DELETE FROM "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
		if !strings.Contains(query, "id = $1 AND station_id = $2 AND group_external_id = $3") || len(args) != 3 || args[0].Value != int64(42) || args[1].Value != int64(1) || args[2].Value != int64(9) {
			t.Errorf("unscoped single delete: %s %+v", query, args)
		}
	}})
	repo, _ := newDegradationSQL(t, steps...)
	if err := repo.DeleteGroupDegradationResult(context.Background(), 1, 9, 42); err != nil {
		t.Fatalf("delete single result: %v", err)
	}
}

func TestDeleteGroupDegradationResultRejectsUnknownScope(t *testing.T) {
	steps := degradationScopeSteps(false)
	steps = steps[:1]
	steps[0].rows = nil
	repo, state := newDegradationSQL(t, steps...)
	if err := repo.DeleteGroupDegradationResult(context.Background(), 1, 9, 42); !errors.Is(err, gorm.ErrRecordNotFound) || state.commits != 0 || state.rollbacks != 0 {
		t.Fatalf("delete outside scope err=%v state=%+v", err, state)
	}
}

func TestValidGroupDegradationStatusAcceptsTrimmedVerdicts(t *testing.T) {
	for _, status := range []string{"normal", "degraded", "inconclusive", "error", " normal "} {
		if !ValidGroupDegradationStatus(status) {
			t.Errorf("rejected verdict %q", status)
		}
	}
	for _, status := range []string{"", " ", "ok", "NORMAL", "degradedx", "degraded;"} {
		if ValidGroupDegradationStatus(status) {
			t.Errorf("accepted verdict %q", status)
		}
	}
}

// degradationResultRow is the locked read of one history row before a manual
// verdict change; only the columns that drive the decision are returned.
func degradationResultRow(id int64, status, original string, adjusted driver.Value) degradationSQLStep {
	return degradationSQLStep{
		contains: `FROM "group_degradation_results"`,
		columns:  []string{"id", "status", "original_status", "adjusted_at"},
		rows:     [][]driver.Value{{id, status, original, adjusted}},
	}
}

func TestUpdateGroupDegradationResultKeepsJudgeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, current, original, set string
		wantStatus, wantOriginal     string
		wantAdjusted                 bool
	}{
		{"override", "degraded", "", "normal", "normal", "degraded", true},
		{"second override keeps automatic verdict", "normal", "degraded", "inconclusive", "inconclusive", "degraded", true},
		{"revert clears marker", "normal", "degraded", "degraded", "degraded", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lock := degradationResultRow(59, tc.current, tc.original, nil)
			lock.contains = "FOR UPDATE"
			lock.check = func(query string, args []driver.NamedValue) {
				if !strings.Contains(query, "id = $1 AND station_id = $2 AND group_external_id = $3") || len(args) != 4 || args[0].Value != int64(59) || args[1].Value != int64(1) || args[2].Value != int64(9) {
					t.Errorf("history row not locked inside the group scope: %s %+v", query, args)
				}
			}
			update := degradationSQLStep{contains: `UPDATE "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
				if status, ok := degradationUpdateArg(args, query, "status"); !ok || status != tc.wantStatus {
					t.Errorf("status=%v want=%q query=%s", status, tc.wantStatus, query)
				}
				if original, ok := degradationUpdateArg(args, query, "original_status"); !ok || original != tc.wantOriginal {
					t.Errorf("original_status=%v want=%q query=%s", original, tc.wantOriginal, query)
				}
				adjusted, ok := degradationUpdateArg(args, query, "adjusted_at")
				if !ok {
					t.Fatalf("manual marker missing from update: %s", query)
				}
				if tc.wantAdjusted && adjusted == nil {
					t.Errorf("adjusted_at not stamped: %s", query)
				}
				if !tc.wantAdjusted && adjusted != nil {
					t.Errorf("adjusted_at not cleared: %s", query)
				}
				for _, invalid := range []string{`"reason"`, `"html"`, `"answer"`, `"status" = `} {
					if strings.Contains(query, invalid) {
						t.Errorf("manual review rewrote judge evidence %q: %s", invalid, query)
					}
				}
			}}
			steps := append(degradationScopeSteps(true), lock, update)
			repo, state := newDegradationSQL(t, steps...)
			if err := repo.UpdateGroupDegradationResult(context.Background(), 1, 9, 59, tc.set); err != nil || state.commits != 1 || state.rollbacks != 0 {
				t.Fatalf("update err=%v state=%+v", err, state)
			}
		})
	}
}

func TestUpdateGroupDegradationResultValidatesBeforeWriting(t *testing.T) {
	repo, state := newDegradationSQL(t)
	if err := repo.UpdateGroupDegradationResult(context.Background(), 1, 9, 59, "ok"); !errors.Is(err, ErrInvalidGroupDegradationConfig) || state.commits+state.rollbacks != 0 {
		t.Fatalf("invalid status err=%v state=%+v", err, state)
	}
	unknownStation := degradationScopeSteps(true)[:1]
	unknownStation[0].rows = nil
	repo, state = newDegradationSQL(t, unknownStation...)
	if err := repo.UpdateGroupDegradationResult(context.Background(), 1, 9, 59, "normal"); !errors.Is(err, gorm.ErrRecordNotFound) || state.commits+state.rollbacks != 0 {
		t.Fatalf("unknown station err=%v state=%+v", err, state)
	}
	foreign := degradationResultRow(59, "degraded", "", nil)
	foreign.contains = "FOR UPDATE"
	foreign.rows = nil
	repo, state = newDegradationSQL(t, append(degradationScopeSteps(true), foreign)...)
	if err := repo.UpdateGroupDegradationResult(context.Background(), 1, 9, 59, "normal"); !errors.Is(err, gorm.ErrRecordNotFound) || state.rollbacks != 1 || state.commits != 0 {
		t.Fatalf("result outside scope err=%v state=%+v", err, state)
	}
}
