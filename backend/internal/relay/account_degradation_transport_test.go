package relay

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
)

func TestAccountDegradationAdminEvents(t *testing.T) {
	content := "data: {\"type\":\"content\",\"text\":\"21\"}\n\n"
	complete := "data: {\"type\":\"test_complete\",\"success\":true}\n\n"
	for _, tc := range []struct {
		name string
		wire string
		want error
	}{
		{"complete", content + complete, nil},
		{"missing-completion", content, errDegradationUnfinished},
		{"failed-completion", content + "data: {\"type\":\"test_complete\",\"success\":false}\n\n", errDegradationUnfinished},
		{"empty", complete, errDegradationEmpty},
		{"error", "data: {\"type\":\"error\",\"error\":\"private credential\"}\n\n", errDegradationUpstream},
		{"late-error", content + complete + "event: error\ndata: {}\n\n", errDegradationUpstream},
		{"late-content", content + complete + content, errDegradationUnfinished},
		{"invalid", "data: not json\n\n", errDegradationFormat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readAccountDegradationEvents(strings.NewReader(tc.wire), 100, 4096)
			if !errors.Is(err, tc.want) || (err == nil && got != "21") {
				t.Fatalf("output=%q err=%v want=%v", got, err, tc.want)
			}
		})
	}
	if _, err := readAccountDegradationEvents(strings.NewReader(content+complete), 1, 4096); !errors.Is(err, errDegradationTextLimit) {
		t.Fatalf("unbounded content: %v", err)
	}
}

func TestAccountDegradationAdminUsesConnectionEndpointAndPrompt(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/admin/accounts/11/test" || r.Method != http.MethodPost || r.Header.Get("x-api-key") != "admin-local" {
			t.Errorf("wrong account route: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model_id"] != "candidate" || body["prompt"] != "judge instruction\n\nprivate candy question" || body["mode"] != "regular" {
			t.Errorf("wrong test payload: %+v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"test_start\",\"model\":\"mapped\"}\n\ndata: {\"type\":\"content\",\"text\":\"21\"}\n\ndata: {\"type\":\"test_complete\",\"success\":true}\n\n")
	}))
	defer server.Close()
	s := &Service{client: server.Client()}
	got, err := s.accountDegradationAdminChat(context.Background(), server.URL, "admin-local", 11, "candidate", "none", "judge instruction", "private candy question", time.Second, 100)
	if err != nil || got != "21" || calls != 1 {
		t.Fatalf("output=%q err=%v calls=%d", got, err, calls)
	}
}

func TestAccountDegradationAdminPathDoesNotExportCredentials(t *testing.T) {
	cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("admin-local")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/admin/accounts/11" || r.Header.Get("x-api-key") != "admin-local" {
			t.Errorf("unexpected credential request: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": 11, "type": "apikey", "platform": "openai", "credentials": map[string]any{}, "extra": map[string]any{"openai_responses_supported": false}}})
	}))
	defer server.Close()
	repo, _ := newDegradationRunSQL(t, degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id", "base_url", "api_key_cipher"}, rows: [][]driver.Value{{int64(1), server.URL, encrypted}}})
	s := &Service{stations: repo, cipher: cipher, client: server.Client()}
	_, endpoint, key, protocol, mapping, _, err := s.accountDegradationCredential(context.Background(), 1, 11, "")
	if err != nil || endpoint != server.URL || key != "admin-local" || protocol != "account_test" || mapping != nil || calls != 1 {
		t.Fatalf("endpoint=%s protocol=%s calls=%d err=%v", endpoint, protocol, calls, err)
	}
}

func TestAccountDegradationFallbackModels(t *testing.T) {
	for _, tc := range []struct {
		name, models, fallback, want string
		fail                         bool
	}{
		{"primary", `["candidate","backup"]`, "backup", "candidate", false},
		{"backup", `["backup"]`, "backup", "backup", false},
		{"missing", `["other"]`, "backup", "", true},
		{"read-failed", "", "backup", "", true},
		{"no-fallback-no-preflight", "", "", "candidate", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef")
			if err != nil {
				t.Fatal(err)
			}
			encrypted, _ := cipher.Encrypt("admin-local")
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/admin/accounts/11/models" || r.Method != http.MethodGet {
					t.Errorf("unexpected paid request: %s", r.URL.Path)
				}
				if tc.models == "" {
					w.WriteHeader(503)
					return
				}
				fmt.Fprintf(w, `{"code":0,"data":%s}`, tc.models)
			}))
			defer server.Close()
			var steps []degradationRunSQLStep
			if tc.fallback != "" {
				steps = append(steps, degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id", "base_url", "api_key_cipher"}, rows: [][]driver.Value{{int64(1), server.URL, encrypted}}})
			}
			repo, _ := newDegradationRunSQL(t, steps...)
			s := &Service{stations: repo, cipher: cipher, client: server.Client()}
			config := storage.DefaultGroupDegradationConfig()
			config.Model = "candidate"
			got, err := s.selectAccountDegradationModel(context.Background(), 1, 11, config, tc.fallback)
			if (err != nil) != tc.fail || (err == nil && (got.Model != tc.want || got.JudgeModel != "")) {
				t.Fatalf("model=%s judge=%s err=%v", got.Model, got.JudgeModel, err)
			}
			if tc.fallback == "" && calls != 0 {
				t.Fatal("unexpected preflight")
			}
		})
	}
}

func TestAccountDegradationPromptRouting(t *testing.T) {
	for _, tc := range []struct {
		platform, kind, protocol string
		want                     bool
	}{
		{"openai", "apikey", "chat", true}, {"openai", "apikey", "responses", false}, {"openai", "oauth", "chat", false}, {"anthropic", "apikey", "anthropic", false}, {"gemini", "service_account", "gemini", true}, {"antigravity", "oauth", "", true},
	} {
		account := degradationAccountRemote{remoteAccount: remoteAccount{Platform: tc.platform, Type: tc.kind}}
		if got := accountTestAcceptsPrompt(account, tc.protocol); got != tc.want {
			t.Errorf("%+v got=%v", tc, got)
		}
	}
}
