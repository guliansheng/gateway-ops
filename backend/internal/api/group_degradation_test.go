package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/guliansheng/gateway-ops/internal/relay"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func degradationTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	d := &Deps{Relay: &relay.Service{}}
	registerGroupDegradation(r.Group("/api"), d)
	registerPublicGroupDegradation(r.Group("/api/public"), d)
	return r
}

func TestGroupDegradationRouteContract(t *testing.T) {
	r := degradationTestRouter()
	want := map[string]bool{
		"GET /api/relay-stations/:id/groups/:external_id/degradation":                            true,
		"PUT /api/relay-stations/:id/groups/:external_id/degradation":                            true,
		"POST /api/relay-stations/:id/groups/:external_id/degradation/run":                       true,
		"DELETE /api/relay-stations/:id/groups/:external_id/degradation/history":                 true,
		"DELETE /api/relay-stations/:id/groups/:external_id/degradation/history/:result_id":      true,
		"PATCH /api/relay-stations/:id/groups/:external_id/degradation/history/:result_id":       true,
		"POST /api/relay-stations/:id/groups/:external_id/degradation/history/:result_id/review": true,
		"GET /api/relay-stations/:id/degradation-prompts":                                        true,
		"PUT /api/relay-stations/:id/degradation-prompts":                                        true,
		"GET /api/public/relay-stations/:id/groups/:external_id/degradation":                     true,
		"GET /api/relay-stations/:id/accounts/:external_id/degradation":                          true,
		"POST /api/relay-stations/:id/accounts/:external_id/degradation/run":                     true,
		"GET /api/relay-stations/:id/accounts/:external_id/degradation/config":                   true,
		"PUT /api/relay-stations/:id/accounts/:external_id/degradation/config":                   true,
		"GET /api/relay-stations/:id/account-degradation/latest":                                 true,
		"DELETE /api/relay-stations/:id/accounts/:external_id/degradation/history":               true,
		"DELETE /api/relay-stations/:id/accounts/:external_id/degradation/history/:result_id":    true,
		"GET /api/relay-stations/:id/degradation-usage":                                          true,
	}
	for _, route := range r.Routes() {
		key := route.Method + " " + route.Path
		if !want[key] {
			t.Fatalf("unexpected route %s", key)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %v", want)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/public/relay-stations/1/groups/9/degradation/run", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("public execution/mutation exposed: %s = %d", method, w.Code)
		}
	}
}

// Both review endpoints must reject a bad id/status/body before touching the
// database: the test router has no storage service at all.
func TestGroupDegradationReviewAndImportValidation(t *testing.T) {
	r := degradationTestRouter()
	cases := []struct{ method, path, body string }{
		{http.MethodPatch, "/api/relay-stations/1/groups/9/degradation/history/abc", `{"status":"normal"}`},
		{http.MethodPatch, "/api/relay-stations/1/groups/9/degradation/history/0", `{"status":"normal"}`},
		{http.MethodPatch, "/api/relay-stations/1/groups/9/degradation/history/5", `{}`},
		{http.MethodPatch, "/api/relay-stations/1/groups/9/degradation/history/5", `{"status":"ok"}`},
		{http.MethodPatch, "/api/relay-stations/1/groups/9/degradation/history/5", `{"status":null}`},
		{http.MethodPatch, "/api/relay-stations/1/groups/9/degradation/history/5", `{"status":"normal","api_key":"secret"}`},
	}
	for _, tc := range cases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s body=%s code=%d", tc.method, tc.path, tc.body, w.Code)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("request credential echoed")
		}
	}
}

func TestGroupDegradationRejectsInvalidIDsAndBodiesBeforeIO(t *testing.T) {
	r := degradationTestRouter()
	for _, path := range []string{
		"/api/relay-stations/0/groups/1/degradation",
		"/api/relay-stations/1/groups/-1/degradation",
		"/api/public/relay-stations/nope/groups/1/degradation",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("path=%s code=%d", path, w.Code)
		}
	}
	for _, body := range []string{"null", "[]", `{`, `{"config":{}}`, `{"api_key":"secret"}`, `{} {}`, `{"interval_minutes":"15"}`, `{"model":"` + strings.Repeat("a", 17000) + `"}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/relay-stations/1/groups/9/degradation", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body prefix=%.25s code=%d", body, w.Code)
		}
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatal("request credential echoed")
		}
	}
}

func TestGroupDegradationConfigBodyContract(t *testing.T) {
	config := storage.DefaultGroupDegradationConfig()
	err := decodeGroupDegradationConfig([]byte(`{"enabled":true,"method":"candy","model":"candidate","judge_model":"reviewer","effort":"none","interval_minutes":5,"preview_mode":"hover","result_visibility":"hide"}`), &config)
	if err != nil || !config.Enabled || config.Method != "candy" || config.Model != "candidate" || config.JudgeModel != "reviewer" || config.Effort != "none" || config.IntervalMinutes != 5 || config.PreviewMode != "hover" || config.ResultVisibility != "hide" {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	if err := decodeGroupDegradationConfig([]byte(`{"config":{}}`), &config); err == nil {
		t.Fatal("accepted wrapper instead of Config")
	}
	legacy := storage.DefaultGroupDegradationConfig()
	if err := decodeGroupDegradationConfig([]byte(`{"preview_mode":"click"}`), &legacy); err != nil || legacy.PreviewMode != "click" || legacy.ResultVisibility != "show" {
		t.Fatalf("legacy config=%+v err=%v", legacy, err)
	}
}

func TestGroupDegradationErrorContractAndRedaction(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{gorm.ErrRecordNotFound, http.StatusNotFound},
		{storage.ErrGroupDegradationDisabled, http.StatusConflict},
		{storage.ErrGroupDegradationBusy, http.StatusConflict},
		{storage.ErrInvalidGroupDegradationConfig, http.StatusBadRequest},
		{errors.New("upstream Bearer secret raw error"), http.StatusInternalServerError},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		failGroupDegradation(c, tc.err)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "raw error") {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestDegradationPromptsPrivateRouteAndInvalidRequests(t *testing.T) {
	r := degradationTestRouter()
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete} {
		for _, path := range []string{"/api/public/relay-stations/1/degradation-prompts", "/api/public/relay-stations/1/groups/9/degradation/history"} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
			if w.Code != http.StatusNotFound {
				t.Fatalf("public settings/clear exposed: %s %s code=%d", method, path, w.Code)
			}
		}
	}
	for _, path := range []string{"/api/relay-stations/0/degradation-prompts", "/api/relay-stations/nope/degradation-prompts", "/api/relay-stations/18446744073709551616/degradation-prompts"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("invalid ID path=%s code=%d headers=%v", path, w.Code, w.Header())
		}
	}
	for _, body := range []string{
		`null`, `[]`, `"string"`, `{`, `{}`, `{} {}`, `{} trailing`,
		`{"candy_prompt":null,"candy_expected_answer":"reference","pelican_prompt":"tree"}`,
		`{"candy_prompt":"question","candy_expected_answer":null,"pelican_prompt":"tree"}`,
		`{"candy_prompt":"question","candy_expected_answer":"reference","pelican_prompt":null}`,
		`{"candy_prompt":"question","candy_expected_answer":"reference","pelican_prompt":"tree","unknown":"secret"}`,
		`{"candy_prompt":42,"candy_expected_answer":"reference","pelican_prompt":"tree"}`,
		`{"candy_prompt":"question\u0000","candy_expected_answer":"reference","pelican_prompt":"tree"}`,
		`{"candy_prompt":"` + strings.Repeat("a", 32*1024+1) + `","candy_expected_answer":"reference","pelican_prompt":"tree"}`,
		`{"candy_prompt":"question","candy_expected_answer":"` + strings.Repeat("a", 4*1024+1) + `","pelican_prompt":"tree"}`,
		`{"candy_prompt":"` + strings.Repeat("a", degradationPromptsBodyLimit) + `","candy_expected_answer":"reference","pelican_prompt":"tree"}`,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/relay-stations/1/degradation-prompts", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("body prefix=%.35s code=%d headers=%v", body, w.Code, w.Header())
		}
		if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "reference") || strings.Contains(w.Body.String(), "question") {
			t.Errorf("private body echoed: %s", w.Body.String())
		}
	}
}

func TestDegradationPromptsStrictDecoderAndHistoryLimit(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{"enabled":null}`, `{"history_limit":null}`, `{"history_limit":1.5}`, `{"history_limit":"18"}`, `{"history_limit":18} {}`, `{"history_limit":18} trailing`} {
		config := storage.DefaultGroupDegradationConfig()
		if err := decodeGroupDegradationConfig([]byte(body), &config); err == nil {
			t.Errorf("strict config decoder accepted %s", body)
		}
	}
	config := storage.DefaultGroupDegradationConfig()
	if err := decodeGroupDegradationConfig([]byte(`{"history_limit":18}`), &config); err != nil || config.HistoryLimit != 18 {
		t.Fatalf("history limit contract=%+v err=%v", config, err)
	}
	legacy := storage.DefaultGroupDegradationConfig()
	if err := decodeGroupDegradationConfig([]byte(`{"enabled":false}`), &legacy); err != nil || legacy.HistoryLimit != 20 {
		t.Fatalf("legacy retention=%+v err=%v", legacy, err)
	}
	var prompts storage.DegradationPromptSettings
	if err := decodeDegradationObject(strings.NewReader(`{"candy_prompt":"question","candy_expected_answer":"42","pelican_prompt":"tree"}`), &prompts); err != nil || prompts.CandyPrompt != "question" || prompts.CandyExpectedAnswer != "42" || prompts.PelicanPrompt != "tree" {
		t.Fatalf("prompt JSON=%+v err=%v", prompts, err)
	}
}

func TestDegradationPromptsErrorContractNoLeak(t *testing.T) {
	for _, tc := range []struct {
		err     error
		writing bool
		code    int
	}{
		{gorm.ErrRecordNotFound, false, http.StatusNotFound},
		{storage.ErrInvalidDegradationPromptSettings, true, http.StatusBadRequest},
		{storage.ErrInvalidDegradationPromptSettings, false, http.StatusInternalServerError},
		{errors.New("database password secret reference answer"), false, http.StatusInternalServerError},
		{errors.New("database password secret reference answer"), true, http.StatusInternalServerError},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		failDegradationPrompts(c, tc.err, tc.writing)
		if w.Code != tc.code || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "reference") {
			t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
		}
	}
}

type degradationAPISQLStep struct {
	contains string
	columns  []string
	rows     [][]driver.Value
	err      error
	check    func(string, []driver.NamedValue)
}
type degradationAPISQL struct {
	t                  *testing.T
	steps              []degradationAPISQLStep
	commits, rollbacks int
}
type degradationAPIConnector struct{ state *degradationAPISQL }
type degradationAPIDriver struct{}
type degradationAPIConn struct{ state *degradationAPISQL }
type degradationAPITx struct{ state *degradationAPISQL }
type degradationAPIRows struct {
	columns []string
	rows    [][]driver.Value
}

func (c degradationAPIConnector) Connect(context.Context) (driver.Conn, error) {
	return &degradationAPIConn{state: c.state}, nil
}
func (c degradationAPIConnector) Driver() driver.Driver { return degradationAPIDriver{} }
func (degradationAPIDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}
func (c *degradationAPIConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *degradationAPIConn) Close() error { return nil }
func (c *degradationAPIConn) Begin() (driver.Tx, error) {
	return &degradationAPITx{state: c.state}, nil
}
func (c *degradationAPIConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (tx *degradationAPITx) Commit() error   { tx.state.commits++; return nil }
func (tx *degradationAPITx) Rollback() error { tx.state.rollbacks++; return nil }
func (c *degradationAPIConn) step(query string, args []driver.NamedValue) degradationAPISQLStep {
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
func (c *degradationAPIConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step := c.step(query, args)
	if step.err != nil {
		return nil, step.err
	}
	return &degradationAPIRows{columns: step.columns, rows: step.rows}, nil
}
func (c *degradationAPIConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step := c.step(query, args)
	return driver.RowsAffected(1), step.err
}
func (r *degradationAPIRows) Columns() []string { return r.columns }
func (r *degradationAPIRows) Close() error      { return nil }
func (r *degradationAPIRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}
func newDegradationAPISQL(t *testing.T, steps ...degradationAPISQLStep) (*gin.Engine, *degradationAPISQL) {
	t.Helper()
	state := &degradationAPISQL{t: t, steps: steps}
	sqlDB := sql.OpenDB(degradationAPIConnector{state: state})
	t.Cleanup(func() {
		_ = sqlDB.Close()
		if len(state.steps) != 0 {
			t.Errorf("%d SQL steps not consumed", len(state.steps))
		}
	})
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	d := &Deps{Relay: relay.NewService(storage.NewRelayStations(db), nil)}
	registerGroupDegradation(r.Group("/api"), d)
	registerPublicGroupDegradation(r.Group("/api/public"), d)
	return r, state
}
func degradationAPIStationStep() degradationAPISQLStep {
	return degradationAPISQLStep{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}}
}

// 清空 / 删除 / 改判都会重新读一次管理端视图，测试要按真实顺序排队：
// 站点 → 分组 → 配置 → 成功桶 → 失败桶。
func degradationAPIViewSteps(historyLimit int, completedRows [][]driver.Value) []degradationAPISQLStep {
	return []degradationAPISQLStep{
		degradationAPIStationStep(),
		{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}},
		{contains: `FROM "group_degradation_configs"`, columns: []string{"id", "station_id", "group_external_id", "enabled", "history_limit"}, rows: [][]driver.Value{{int64(7), int64(1), int64(9), true, int64(historyLimit)}}},
		{contains: `FROM "group_degradation_results"`, columns: []string{"id"}, rows: completedRows},
		{contains: `FROM "group_degradation_results"`, columns: []string{"id"}},
	}
}

func TestDegradationPromptsGetPutResponseAndStationErrors(t *testing.T) {
	for _, tc := range []struct {
		method                   string
		stored, missing, dbError bool
	}{
		{method: http.MethodGet}, {method: http.MethodGet, stored: true}, {method: http.MethodPut},
		{method: http.MethodGet, missing: true}, {method: http.MethodPut, missing: true},
		{method: http.MethodGet, dbError: true}, {method: http.MethodPut, dbError: true},
	} {
		t.Run(fmt.Sprintf("%s/stored=%v/missing=%v/error=%v", tc.method, tc.stored, tc.missing, tc.dbError), func(t *testing.T) {
			station := degradationAPIStationStep()
			if tc.missing {
				station.rows = nil
			}
			steps := []degradationAPISQLStep{station}
			if !tc.missing {
				if tc.method == http.MethodGet {
					lookup := degradationAPISQLStep{contains: `FROM "degradation_prompt_settings"`, columns: []string{"candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference"}}
					if tc.stored {
						lookup.rows = [][]driver.Value{{"stored question", "stored reference", "stored drawing", "stored checklist"}}
					}
					if tc.dbError {
						lookup.err = errors.New("database password private secret")
					}
					steps = append(steps, lookup)
				} else {
					upsert := degradationAPISQLStep{contains: `ON CONFLICT ("station_id") DO UPDATE SET`, columns: []string{"id"}, rows: [][]driver.Value{{int64(7)}}, check: func(_ string, args []driver.NamedValue) {
						if args[0].Value != int64(1) || args[1].Value != "custom question" || args[2].Value != "42" || args[3].Value != "custom drawing" || args[4].Value != storage.DefaultPelicanDegradationReference {
							t.Errorf("PUT not station-owned/trimmed: %+v", args)
						}
					}}
					if tc.dbError {
						upsert.err = errors.New("database password private secret")
					}
					steps = append(steps, upsert)
				}
			}
			r, _ := newDegradationAPISQL(t, steps...)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(tc.method, "/api/relay-stations/1/degradation-prompts", strings.NewReader(`{"candy_prompt":" custom question ","candy_expected_answer":" 42 ","pelican_prompt":" custom drawing "}`)))
			wantCode := http.StatusOK
			if tc.missing {
				wantCode = http.StatusNotFound
			} else if tc.dbError {
				wantCode = http.StatusInternalServerError
			}
			if w.Code != wantCode || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "password") {
				t.Fatalf("code=%d body=%s headers=%v", w.Code, w.Body.String(), w.Header())
			}
			if wantCode != http.StatusOK {
				return
			}
			var envelope map[string]map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || len(envelope) != 1 || len(envelope["data"]) != 4 {
				t.Fatalf("prompt response envelope=%s err=%v", w.Body.String(), err)
			}
			want := storage.DefaultDegradationPromptSettings()
			if tc.stored {
				want = storage.DegradationPromptSettings{CandyPrompt: "stored question", CandyExpectedAnswer: "stored reference", PelicanPrompt: "stored drawing", PelicanReference: "stored checklist"}
			} else if tc.method == http.MethodPut {
				want = storage.DegradationPromptSettings{CandyPrompt: "custom question", CandyExpectedAnswer: "42", PelicanPrompt: "custom drawing", PelicanReference: storage.DefaultPelicanDegradationReference}
			}
			if envelope["data"]["candy_prompt"] != want.CandyPrompt || envelope["data"]["candy_expected_answer"] != want.CandyExpectedAnswer || envelope["data"]["pelican_prompt"] != want.PelicanPrompt || envelope["data"]["pelican_reference"] != want.PelicanReference {
				t.Fatalf("response=%s", w.Body.String())
			}
		})
	}
}

func TestDegradationPromptsBodyLimitIncludesTrailingWhitespace(t *testing.T) {
	base := `{"candy_prompt":"question","candy_expected_answer":"42","pelican_prompt":"tree"}`
	for _, over := range []bool{false, true} {
		t.Run(fmt.Sprint(over), func(t *testing.T) {
			var steps []degradationAPISQLStep
			if !over {
				steps = []degradationAPISQLStep{degradationAPIStationStep(), {contains: `INSERT INTO "degradation_prompt_settings"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(7)}}}}
			}
			r, _ := newDegradationAPISQL(t, steps...)
			size := degradationPromptsBodyLimit
			if over {
				size++
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/relay-stations/1/degradation-prompts", strings.NewReader(base+strings.Repeat(" ", size-len(base)))))
			want := http.StatusOK
			if over {
				want = http.StatusBadRequest
			}
			if w.Code != want {
				t.Fatalf("body size=%d code=%d body=%s", size, w.Code, w.Body.String())
			}
		})
	}
}

func TestGroupDegradationClearHistoryHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		busy, missingConfig, missingGroup, dbError bool
	}{
		{name: "clear"}, {name: "busy", busy: true}, {name: "no config", missingConfig: true}, {name: "wrong group", missingGroup: true}, {name: "database error", dbError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := degradationAPISQLStep{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}}
			if tc.missingGroup {
				group.rows = nil
			}
			steps := []degradationAPISQLStep{degradationAPIStationStep(), group}
			if !tc.missingGroup {
				var lease driver.Value
				if tc.busy {
					lease = time.Now().Add(time.Minute)
				}
				config := degradationAPISQLStep{contains: "FOR UPDATE", columns: []string{"id", "station_id", "group_external_id", "enabled", "history_limit", "lease_until"}, rows: [][]driver.Value{{int64(7), int64(1), int64(9), true, int64(27), lease}}}
				if tc.missingConfig {
					config.rows = nil
				}
				steps = append(steps, config)
				if !tc.busy && !tc.missingConfig {
					clear := degradationAPISQLStep{contains: `DELETE FROM "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
						if !strings.Contains(query, "station_id = $1 AND group_external_id = $2") || len(args) != 2 || args[0].Value != int64(1) || args[1].Value != int64(9) {
							t.Errorf("unscoped clear SQL=%s args=%+v", query, args)
						}
					}}
					if tc.dbError {
						clear.err = errors.New("database private secret")
					}
					steps = append(steps, clear)
				}
			}
			// 清空成功后会重读视图：只清“错误”时剩下的成功记录必须一起返回。
			wantResults := 0
			if !tc.busy && !tc.missingGroup && !tc.dbError {
				if tc.missingConfig {
					steps = append(steps, degradationAPIStationStep(),
						degradationAPISQLStep{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}},
						degradationAPISQLStep{contains: `FROM "group_degradation_configs"`, columns: []string{"id"}})
				} else {
					steps = append(steps, degradationAPIViewSteps(27, [][]driver.Value{{int64(5)}})...)
					wantResults = 1
				}
			}
			r, state := newDegradationAPISQL(t, steps...)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/relay-stations/1/groups/9/degradation/history", nil))
			wantCode := http.StatusOK
			if tc.busy {
				wantCode = http.StatusConflict
			} else if tc.missingGroup {
				wantCode = http.StatusNotFound
			} else if tc.dbError {
				wantCode = http.StatusInternalServerError
			}
			if w.Code != wantCode || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
			if wantCode != http.StatusOK {
				if !tc.missingGroup && state.rollbacks != 1 {
					t.Fatalf("failed clear didn't rollback")
				}
				return
			}
			var response struct {
				Data storage.GroupDegradationView `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Data.Results == nil || len(response.Data.Results) != wantResults || state.commits != 1 {
				t.Fatalf("clear response=%s err=%v", w.Body.String(), err)
			}
			wantLimit := 27
			if tc.missingConfig {
				wantLimit = 20
			}
			if response.Data.Config.HistoryLimit != wantLimit || strings.Contains(w.Body.String(), "candy_prompt") || strings.Contains(w.Body.String(), "candy_expected_answer") {
				t.Fatalf("clear config/secret contract=%s", w.Body.String())
			}
		})
	}
}

// 清空接口把 filter 原样透传：非法筛选返回 400 且不落库，
// 前端的“清空当前列表”依赖这条约定，不能退化成删全表。
func TestGroupDegradationClearHistoryHTTPFilter(t *testing.T) {
	for _, tc := range []struct {
		filter   string
		wantCode int
		scope    string
	}{
		{filter: "error", wantCode: http.StatusOK, scope: "status = $"},
		{filter: "success", wantCode: http.StatusOK, scope: "status <> $"},
		{filter: "candy", wantCode: http.StatusOK, scope: "method = $"},
		{filter: "全部", wantCode: http.StatusBadRequest},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			group := degradationAPISQLStep{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}}
			config := degradationAPISQLStep{contains: "FOR UPDATE", columns: []string{"id", "station_id", "group_external_id", "enabled", "history_limit", "lease_until"}, rows: [][]driver.Value{{int64(7), int64(1), int64(9), true, int64(27), nil}}}
			steps := []degradationAPISQLStep{degradationAPIStationStep(), group, config}
			if tc.wantCode == http.StatusOK {
				steps = append(steps, degradationAPISQLStep{contains: `DELETE FROM "group_degradation_results"`, check: func(query string, args []driver.NamedValue) {
					if !strings.Contains(query, tc.scope) || len(args) != 3 || args[1].Value != int64(1) || args[2].Value != int64(9) {
						t.Errorf("filtered clear SQL=%s args=%+v", query, args)
					}
				}})
			}
			if tc.wantCode == http.StatusOK {
				steps = append(steps, degradationAPIViewSteps(27, nil)...)
			}
			r, state := newDegradationAPISQL(t, steps...)
			w := httptest.NewRecorder()
			target := "/api/relay-stations/1/groups/9/degradation/history?filter=" + url.QueryEscape(tc.filter)
			r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, target, nil))
			if w.Code != tc.wantCode {
				t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				if state.commits != 1 {
					t.Fatalf("filtered clear didn't commit")
				}
			} else if state.rollbacks != 1 || state.commits != 0 {
				t.Fatalf("rejected filter committed: commits=%d rollbacks=%d", state.commits, state.rollbacks)
			}
		})
	}
}

func TestDegradationPromptsDuplicateNullFieldIsRejected(t *testing.T) {
	var prompts storage.DegradationPromptSettings
	for _, body := range []string{
		`{"candy_prompt":null,"candy_prompt":"question","candy_expected_answer":"42","pelican_prompt":"tree"}`,
		`{"candy_prompt":"question","candy_prompt":"other","candy_expected_answer":"42","pelican_prompt":"tree"}`,
	} {
		if err := decodeDegradationObject(strings.NewReader(body), &prompts); err == nil {
			t.Errorf("accepted duplicate/null field: %s", body)
		}
	}
}

func TestAccountDegradationAdminOnlyAndInvalidRequests(t *testing.T) {
	r := degradationTestRouter()
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(method, "/api/public/relay-stations/1/accounts/11/degradation/run", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("public account test exposed: %s %d", method, w.Code)
		}
	}
	for _, body := range []string{`null`, `[]`, `{}`, `{"model":""}`, `{"model":null}`, `{"model":"candidate","method":"unknown"}`, `{"model":"candidate","api_key":"secret"}`, `{} {}`} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/relay-stations/1/accounts/11/degradation/run", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("body=%.20s code=%d output=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestAccountDegradationRunRequestDecoding(t *testing.T) {
	request := accountDegradationRunRequest{GroupDegradationConfig: storage.DefaultGroupDegradationConfig()}
	if err := decodeDegradationObject(strings.NewReader(`{"model":"candidate","fallback_model":"backup","protocol":"chat"}`), &request); err != nil || request.Model != "candidate" || request.FallbackModel != "backup" || request.Protocol != "chat" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	for _, body := range []string{`{"model":"candidate","fallback_model":null}`, `{"model":"candidate","protocol":null}`, `{"model":"candidate","fallback_model":"a","fallback_model":"b"}`} {
		if err := decodeDegradationObject(strings.NewReader(body), &request); err == nil {
			t.Fatalf("accepted invalid request: %s", body)
		}
	}
}

func TestDegradationSingleDeleteRejectsInvalidResultID(t *testing.T) {
	r := degradationTestRouter()
	for _, path := range []string{
		"/api/relay-stations/1/groups/9/degradation/history/abc",
		"/api/relay-stations/1/groups/9/degradation/history/0",
		"/api/relay-stations/1/accounts/11/degradation/history/abc",
		"/api/relay-stations/1/accounts/11/degradation/history/0",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, path, nil))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "检测记录 ID 无效") {
			t.Fatalf("%s = %d %s", path, w.Code, w.Body.String())
		}
	}
}
