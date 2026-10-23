package relay

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestAccountDegradationProtocolAndMapping(t *testing.T) {
	for _, tc := range []struct {
		extra map[string]any
		want  string
	}{
		{nil, "responses"}, {map[string]any{"openai_responses_supported": false}, "chat"},
		{map[string]any{"openai_responses_mode": "force_responses", "openai_responses_supported": false}, "responses"},
		{map[string]any{"openai_responses_mode": "force_chat_completions", "openai_responses_supported": true}, "chat"},
	} {
		if got := accountDegradationProtocol(tc.extra); got != tc.want {
			t.Fatalf("protocol=%s want=%s", got, tc.want)
		}
	}
	mapping := map[string]any{"gpt-*": "upstream", "gpt-6*": "specific", "gpt-6": "exact"}
	for model, want := range map[string]string{"gpt-6": "exact", "gpt-6-test": "specific", "gpt-5": "upstream", "other": "other"} {
		if got := mappedDegradationModel(model, mapping); got != want {
			t.Fatalf("map %s => %s", model, got)
		}
	}
}

func TestAccountDegradationNativeProtocols(t *testing.T) {
	for _, protocol := range []string{"anthropic", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if !strings.Contains(fmt.Sprint(body), "private test question") {
					t.Error("missing test question")
				}
				w.Header().Set("Content-Type", "application/json")
				if protocol == "anthropic" {
					if r.Header.Get("x-api-key") != "key" || r.URL.Path != "/v1/messages" {
						t.Errorf("wrong native request: %s", r.URL.Path)
					}
					fmt.Fprint(w, `{"stop_reason":"end_turn","content":[{"type":"text","text":"answer"}]}`)
				} else {
					if r.Header.Get("x-goog-api-key") != "key" || r.URL.Path != "/v1beta/models/model:generateContent" {
						t.Errorf("wrong Gemini request: %s", r.URL.Path)
					}
					fmt.Fprint(w, `{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"hidden thought","thought":true},{"text":"answer"}]}}]}`)
				}
			}))
			defer server.Close()
			s := &Service{client: server.Client()}
			output, err := s.nativeAccountDegradationChat(context.Background(), server.URL, "key", protocol, "model", "none", "judge instruction", "private test question", time.Second, 1024)
			if err != nil || output != "answer" {
				t.Fatalf("output=%s err=%v", output, err)
			}
		})
	}
}

func TestAccountDegradationNativeRejectsTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"stop_reason":"max_tokens","content":[{"type":"text","text":"partial"}]}`)
	}))
	defer server.Close()
	s := &Service{client: server.Client()}
	if _, err := s.nativeAccountDegradationChat(context.Background(), server.URL, "key", "anthropic", "model", "none", "", "prompt", time.Second, 1024); err == nil {
		t.Fatal("accepted truncated response")
	}
}

func TestAccountDegradationUsesOnlySelectedCredential(t *testing.T) {
	cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("admin-local")
	if err != nil {
		t.Fatal(err)
	}
	upstreamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/admin/accounts/11" {
			if r.Header.Get("x-api-key") != "admin-local" {
				t.Error("missing admin auth")
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": 11, "type": "apikey", "platform": "openai", "credentials": map[string]any{"base_url": "https://selected.invalid/v1", "api_key": "selected-key", "model_mapping": map[string]string{"candidate": "mapped-candidate"}}, "extra": map[string]any{"openai_responses_supported": false}}})
			return
		}
		upstreamCalls++
		t.Errorf("unexpected upstream call %s", r.URL.Path)
	}))
	defer server.Close()
	repo, _ := newDegradationRunSQL(t, degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id", "base_url", "api_key_cipher"}, rows: [][]driver.Value{{int64(1), server.URL, encrypted}}})
	s := &Service{stations: repo, cipher: cipher, client: server.Client()}
	_, endpoint, key, protocol, mapping, secrets, err := s.accountDegradationCredential(context.Background(), 1, 11)
	if err != nil || endpoint != "https://selected.invalid" || key != "selected-key" || protocol != "chat" || mappedDegradationModel("candidate", mapping) != "mapped-candidate" || len(secrets) < 2 || upstreamCalls != 0 {
		t.Fatalf("endpoint=%s protocol=%s err=%v", endpoint, protocol, err)
	}
}

func TestAccountDegradationPromptFailureStillSavesManualError(t *testing.T) {
	until := time.Now().Add(time.Minute)
	lease := func() degradationRunSQLStep {
		return degradationRunSQLStep{contains: "FOR UPDATE", columns: []string{"id", "station_id", "account_external_id", "lease_token", "lease_until"}, rows: [][]driver.Value{{int64(7), int64(1), int64(11), "lease-token", nil}}}
	}
	finish := lease()
	finish.rows[0][4] = until
	// Claim creates a random token; capture it from its UPDATE and return it at completion.
	var token string
	finish.check = func(_ context.Context, _ string, _ []driver.NamedValue) { finish.rows[0][3] = token }
	steps := []degradationRunSQLStep{
		{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
		{contains: `FROM "relay_accounts"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(2)}}},
		{contains: "ON CONFLICT", columns: []string{"id"}}, lease(),
		{contains: `UPDATE "account_degradation_runs"`, check: func(_ context.Context, _ string, args []driver.NamedValue) {
			for _, arg := range args {
				if text, ok := arg.Value.(string); ok && len(text) == 48 {
					token = text
				}
			}
		}},
		{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(1)}}},
		{contains: `FROM "degradation_prompt_settings"`, err: fmt.Errorf("private credentials")},
		finish,
		{contains: `INSERT INTO "account_degradation_results"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(99)}}},
		{contains: `DELETE FROM "account_degradation_results"`}, {contains: `UPDATE "account_degradation_runs"`},
	}
	repo, state := newDegradationRunSQL(t, steps...)
	s := &Service{stations: repo}
	config := storage.DefaultGroupDegradationConfig()
	config.Model = "candidate"
	result, err := s.RunAccountDegradation(context.Background(), 1, 11, config)
	if err != nil || result.ID != 99 || result.Status != "error" || result.Reason != "读取中转站降智测试标准失败" || state.commits != 2 {
		t.Fatalf("result=%+v err=%v state=%+v", result, err, state)
	}
}
