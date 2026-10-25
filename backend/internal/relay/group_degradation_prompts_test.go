package relay

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A local SQL connector and in-memory HTTP transport keep these tests entirely
// offline: no real database rows, credentials, or model-provider requests.
type degradationRunSQLStep struct {
	contains string
	columns  []string
	rows     [][]driver.Value
	err      error
	check    func(context.Context, string, []driver.NamedValue)
}
type degradationRunSQL struct {
	t                  *testing.T
	steps              []degradationRunSQLStep
	commits, rollbacks int
}
type degradationRunConnector struct{ state *degradationRunSQL }
type degradationRunDriver struct{}
type degradationRunConn struct{ state *degradationRunSQL }
type degradationRunTx struct{ state *degradationRunSQL }
type degradationRunRows struct {
	columns []string
	rows    [][]driver.Value
}

func (c degradationRunConnector) Connect(context.Context) (driver.Conn, error) {
	return &degradationRunConn{state: c.state}, nil
}
func (c degradationRunConnector) Driver() driver.Driver { return degradationRunDriver{} }
func (degradationRunDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}
func (c *degradationRunConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c *degradationRunConn) Close() error { return nil }
func (c *degradationRunConn) Begin() (driver.Tx, error) {
	return &degradationRunTx{state: c.state}, nil
}
func (c *degradationRunConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (tx *degradationRunTx) Commit() error   { tx.state.commits++; return nil }
func (tx *degradationRunTx) Rollback() error { tx.state.rollbacks++; return nil }
func (c *degradationRunConn) step(ctx context.Context, query string, args []driver.NamedValue) degradationRunSQLStep {
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
		step.check(ctx, query, args)
	}
	return step
}
func (c *degradationRunConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step := c.step(ctx, query, args)
	if step.err != nil {
		return nil, step.err
	}
	return &degradationRunRows{columns: step.columns, rows: step.rows}, nil
}
func (c *degradationRunConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step := c.step(ctx, query, args)
	return driver.RowsAffected(1), step.err
}
func (r *degradationRunRows) Columns() []string { return r.columns }
func (r *degradationRunRows) Close() error      { return nil }
func (r *degradationRunRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}
func newDegradationRunSQL(t *testing.T, steps ...degradationRunSQLStep) (*storage.RelayStations, *degradationRunSQL) {
	t.Helper()
	state := &degradationRunSQL{t: t, steps: steps}
	sqlDB := sql.OpenDB(degradationRunConnector{state: state})
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
	return storage.NewRelayStations(db), state
}
func degradationRunConfigStep() degradationRunSQLStep {
	return degradationRunSQLStep{contains: "FOR UPDATE", columns: []string{"id", "station_id", "group_external_id", "enabled", "method", "model", "judge_model", "effort", "interval_minutes", "history_limit", "lease_token", "lease_until"}, rows: [][]driver.Value{{int64(7), int64(1), int64(9), true, "candy", "candidate", "judge", "none", int64(15), int64(3), "lease-token", time.Now().Add(time.Minute)}}}
}
func degradationRunCompletionSteps(t *testing.T, status string) []degradationRunSQLStep {
	t.Helper()
	return []degradationRunSQLStep{
		degradationRunConfigStep(),
		{contains: `INSERT INTO "group_degradation_results"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(55)}}, check: func(ctx context.Context, _ string, args []driver.NamedValue) {
			if ctx.Err() != nil {
				t.Errorf("completion used cancelled context: %v", ctx.Err())
			}
			if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 11*time.Second {
				t.Errorf("completion context not independently bounded")
			}
			if len(args) != 20 || args[0].Value != int64(1) || args[1].Value != int64(9) || args[6].Value != status {
				t.Errorf("wrong saved result args=%+v", args)
			}
		}},
		{contains: `DELETE FROM "group_degradation_results"`, check: func(_ context.Context, query string, args []driver.NamedValue) {
			if !strings.Contains(query, "ORDER BY created_at DESC,id DESC OFFSET") || len(args) != 10 || args[4].Value != "error" || args[5].Value != int64(3) || args[9].Value != int64(3) {
				t.Errorf("completion retention query=%s args=%+v", query, args)
			}
		}},
		{contains: `UPDATE "group_degradation_configs"`},
	}
}

func TestGroupDegradationRunPromptReadFailureSavesWithoutUpstream(t *testing.T) {
	for _, invalidStored := range []bool{false, true} {
		t.Run(map[bool]string{false: "database failure", true: "invalid stored settings"}[invalidStored], func(t *testing.T) {
			lookup := degradationRunSQLStep{contains: `FROM "degradation_prompt_settings"`, err: errors.New("private database credentials")}
			if invalidStored {
				lookup.err = nil
				lookup.columns = []string{"candy_prompt", "candy_expected_answer", "pelican_prompt"}
				lookup.rows = [][]driver.Value{{"", "42", "tree"}}
			}
			steps := []degradationRunSQLStep{{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}}, lookup}
			steps = append(steps, degradationRunCompletionSteps(t, "error")...)
			repo, state := newDegradationRunSQL(t, steps...)
			calls := 0
			s := &Service{stations: repo, client: &http.Client{Transport: degradationTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected upstream request")
			})}}
			result, err := s.runClaimedGroupDegradation(context.Background(), storage.GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, Method: "candy", Model: "candidate", Effort: "none", LeaseToken: "lease-token"})
			if err != nil || result.Status != "error" || result.ID != 55 || !strings.Contains(result.Reason, "读取降智检测提示词设置失败") || calls != 0 || state.commits != 1 || result.Answer != "" || result.HTML != "" || strings.Contains(result.Reason, "private") {
				t.Fatalf("settings failure result=%+v err=%v calls=%d", result, err, calls)
			}
		})
	}
}

func TestGroupDegradationRunCapturesSettingsOncePerRun(t *testing.T) {
	cipher, err := crypto.NewCipher("offline-degradation-test-cipher")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt("offline-admin-key")
	if err != nil {
		t.Fatal(err)
	}
	settings := storage.DegradationPromptSettings{CandyPrompt: "first prompt", CandyExpectedAnswer: "42", PelicanPrompt: "draw a tree", PelicanReference: "reference standard"}
	currentPrompt := settings.PelicanPrompt
	lookupCount, modelCalls, credentialCalls := 0, 0, 0
	steps := []degradationRunSQLStep{
		{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
		{contains: `FROM "degradation_prompt_settings"`, columns: []string{"candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference"}, rows: [][]driver.Value{{settings.CandyPrompt, settings.CandyExpectedAnswer, settings.PelicanPrompt, settings.PelicanReference}}, check: func(_ context.Context, _ string, args []driver.NamedValue) {
			lookupCount++
			if args[0].Value != int64(1) {
				t.Errorf("wrong station settings: %+v", args)
			}
		}},
		{contains: `FROM "relay_stations"`, columns: []string{"id", "base_url", "api_key_cipher"}, rows: [][]driver.Value{{int64(1), "https://offline.invalid", ciphertext}}},
		{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}},
	}
	steps = append(steps, degradationRunCompletionSteps(t, "normal")...)
	repo, state := newDegradationRunSQL(t, steps...)
	s := &Service{stations: repo, cipher: cipher, client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		if r.Method == http.MethodGet {
			credentialCalls++
			if lookupCount != 1 || r.Header.Get("x-api-key") != "offline-admin-key" {
				t.Errorf("credential discovery before settings read or wrong key")
			}
			// Simulate an administrator changing the station settings after this
			// run has started: the candidate and judge still use the captured pair.
			currentPrompt = "changed prompt after run start"
			_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"key":"offline-group-key","status":"active","group_id":9,"user":{"role":"admin"}}],"pages":1}}`))
		} else {
			modelCalls++
			var body struct {
				Messages []struct{ Role, Content string } `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if modelCalls == 1 {
				if len(body.Messages) != 1 || body.Messages[0].Content != settings.PelicanPrompt || strings.Contains(body.Messages[0].Content, settings.PelicanReference) {
					t.Errorf("generation not captured: %+v", body)
				}
				degradationChatResponse(w, "<html><svg/></html>")
			} else {
				if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, settings.PelicanPrompt) || strings.Contains(body.Messages[1].Content, currentPrompt) || !strings.Contains(body.Messages[1].Content, settings.PelicanReference) {
					t.Errorf("judge not captured: %+v", body)
				}
				degradationChatResponse(w, `{"status":"normal","reason":"源码满足参考标准"}`)
			}
		}
		return w.Result(), nil
	})}}
	result, err := s.runClaimedGroupDegradation(context.Background(), storage.GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, Method: "pelican", Model: "candidate", JudgeModel: "judge", Effort: "none", LeaseToken: "lease-token"})
	if err != nil || result.Status != "normal" || result.ID != 55 || lookupCount != 1 || credentialCalls != 1 || modelCalls != 2 || state.commits != 1 {
		t.Fatalf("result=%+v err=%v settings=%d model=%d", result, err, lookupCount, modelCalls)
	}
}

func TestGroupDegradationRunCancelledPromptReadStillCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	steps := []degradationRunSQLStep{{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}, check: func(context.Context, string, []driver.NamedValue) { cancel() }}}
	steps = append(steps, degradationRunCompletionSteps(t, "error")...)
	repo, state := newDegradationRunSQL(t, steps...)
	s := &Service{stations: repo}
	result, err := s.runClaimedGroupDegradation(ctx, storage.GroupDegradationConfig{ID: 7, StationID: 1, GroupExternalID: 9, Method: "candy", Model: "candidate", Effort: "none", LeaseToken: "lease-token"})
	if err != nil || result.Status != "error" || state.commits != 1 {
		t.Fatalf("cancelled run outcome=%+v err=%v", result, err)
	}
}

func TestGroupDegradationManualCancellationAndCronCancellation(t *testing.T) {
	for _, manual := range []bool{true, false} {
		name := "cron retains cancellation"
		if manual {
			name = "manual survives cancellation"
		}
		t.Run(name, func(t *testing.T) {
			type contextKey string
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey("request"), "kept"))
			defer cancel()
			cipher, err := crypto.NewCipher("offline-degradation-test-cipher")
			if err != nil {
				t.Fatal(err)
			}
			ciphertext, err := cipher.Encrypt("offline-admin-key")
			if err != nil {
				t.Fatal(err)
			}
			claim := degradationRunConfigStep()
			claim.rows[0][10], claim.rows[0][11] = "", nil
			// A drawing run keeps both steps in play: the claim must still reserve
			// enough time for review after generation.
			claim.rows[0][4] = "pelican"
			steps := []degradationRunSQLStep{}
			if manual {
				steps = append(steps,
					degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
					degradationRunSQLStep{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}})
			} else {
				claim.contains = "SKIP LOCKED"
			}
			claim.check = func(ctx context.Context, _ string, _ []driver.NamedValue) {
				if ctx.Err() != nil || ctx.Value(contextKey("request")) != "kept" {
					t.Fatal("claim lost request context")
				}
			}
			var claimedToken string
			var claimedUntil time.Time
			steps = append(steps, claim, degradationRunSQLStep{contains: `UPDATE "group_degradation_configs"`, check: func(_ context.Context, _ string, args []driver.NamedValue) {
				for _, arg := range args {
					switch value := arg.Value.(type) {
					case string:
						if len(value) == 48 {
							claimedToken = value
						}
					case time.Time:
						if until := time.Until(value); until > 11*time.Minute && until <= 12*time.Minute {
							claimedUntil = value
						}
					}
				}
				if claimedToken == "" || claimedUntil.IsZero() {
					t.Errorf("claim did not create a twelve-minute lease: %+v", args)
				}
			}})
			// The run starts after the claim commits; cancel during credential
			// discovery, before any candidate call can finish.
			steps = append(steps,
				degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}, check: func(ctx context.Context, _ string, _ []driver.NamedValue) {
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > 10*time.Minute || time.Until(deadline) < 9*time.Minute || ctx.Value(contextKey("request")) != "kept" {
						t.Fatal("run context must preserve values and have a ten-minute timeout")
					}
				}},
				degradationRunSQLStep{contains: `FROM "degradation_prompt_settings"`, columns: []string{"candy_prompt", "candy_expected_answer", "pelican_prompt"}, rows: [][]driver.Value{{"question", "42", "draw"}}},
				degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id", "base_url", "api_key_cipher"}, rows: [][]driver.Value{{int64(1), "https://offline.invalid", ciphertext}}},
				degradationRunSQLStep{contains: `FROM "relay_groups"`, columns: []string{"relay_station_id", "external_id"}, rows: [][]driver.Value{{int64(1), int64(9)}}})
			status := "error"
			if manual {
				status = "normal"
			}
			completion := degradationRunCompletionSteps(t, status)
			completion[0].check = func(_ context.Context, _ string, _ []driver.NamedValue) {
				completion[0].rows[0][10], completion[0].rows[0][11] = claimedToken, claimedUntil
			}
			steps = append(steps, completion...)
			repo, state := newDegradationRunSQL(t, steps...)
			modelCalls := 0
			s := &Service{stations: repo, cipher: cipher, client: &http.Client{Transport: degradationTestTransport(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				if r.Method == http.MethodGet {
					cancel()
					if manual && r.Context().Err() != nil {
						t.Fatal("manual run cancelled after claim")
					}
					if !manual {
						if r.Context().Err() != context.Canceled {
							t.Fatal("cron run ignored scheduler cancellation")
						}
						return nil, r.Context().Err()
					}
					_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"key":"offline-group-key","status":"active","group_id":9,"user":{"role":"admin"}}],"pages":1}}`))
				} else {
					modelCalls++
					if r.Context().Err() != nil || r.Context().Value(contextKey("request")) != "kept" {
						t.Fatal("manual model call lost detached context")
					}
					if modelCalls == 1 {
						degradationChatResponse(w, "<html><svg/></html>")
					} else {
						degradationChatResponse(w, `{"status":"normal","reason":"最终结论一致"}`)
					}
				}
				return w.Result(), nil
			})}}
			if manual {
				result, err := s.RunGroupDegradation(ctx, 1, 9)
				if err != nil || result.Status != "normal" || modelCalls != 2 {
					t.Fatalf("manual result=%+v error=%v model calls=%d", result, err, modelCalls)
				}
			} else if err := s.RunDueGroupDegradation(ctx); err != nil || modelCalls != 0 {
				t.Fatalf("cron error=%v model calls=%d", err, modelCalls)
			}
			if state.commits != 2 {
				t.Fatalf("claim and result must both commit: %d", state.commits)
			}
			first, ok := s.acquireGroupDegradationSlot()
			if !ok {
				t.Fatal("run leaked its first slot")
			}
			defer first()
			second, ok := s.acquireGroupDegradationSlot()
			if !ok {
				t.Fatal("run leaked its second slot")
			}
			defer second()
		})
	}
}
