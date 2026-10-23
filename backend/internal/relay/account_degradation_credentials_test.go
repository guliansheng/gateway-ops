package relay

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/guliansheng/gateway-ops/internal/crypto"
)

func TestAccountDegradationRedactedCredentialsExport(t *testing.T) {
	for _, mode := range []string{"allowed", "step-up-required", "wrong-account", "multiple-accounts", "missing-key"} {
		t.Run(mode, func(t *testing.T) {
			cipher, err := crypto.NewCipher("0123456789abcdef0123456789abcdef")
			if err != nil {
				t.Fatal(err)
			}
			encrypted, err := cipher.Encrypt("admin-local")
			if err != nil {
				t.Fatal(err)
			}
			exports := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("x-api-key") != "admin-local" {
					t.Error("missing admin auth")
				}
				if r.URL.Path == "/api/v1/admin/accounts/11" {
					json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"id": 11, "name": "selected", "type": "apikey", "platform": "openai", "credentials": map[string]any{"base_url": "https://selected.invalid/v1", "model_mapping": map[string]string{"candidate": "mapped"}}}})
					return
				}
				if r.URL.Path != "/api/v1/admin/accounts/data" || r.URL.Query().Get("ids") != "11" || r.URL.Query().Get("include_proxies") != "false" || len(r.URL.Query()) != 2 {
					t.Fatalf("unscoped credential read: %s", r.URL)
				}
				exports++
				if mode == "step-up-required" {
					w.WriteHeader(http.StatusForbidden)
					w.Write([]byte(`{"code":"STEP_UP_REQUIRED","message":"private details"}`))
					return
				}
				name, key := "selected", "private-account-key"
				if mode == "wrong-account" {
					name = "another"
				}
				if mode == "missing-key" {
					key = ""
				}
				account := map[string]any{"name": name, "type": "apikey", "platform": "openai", "credentials": map[string]any{"api_key": key}}
				accounts := []any{account}
				if mode == "multiple-accounts" {
					accounts = append(accounts, account)
				}
				json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"accounts": accounts}})
			}))
			defer server.Close()
			repo, _ := newDegradationRunSQL(t, degradationRunSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id", "base_url", "api_key_cipher"}, rows: [][]driver.Value{{int64(1), server.URL, encrypted}}})
			s := &Service{stations: repo, cipher: cipher, client: server.Client()}
			_, endpoint, key, _, mapping, _, err := s.accountDegradationCredential(context.Background(), 1, 11)
			if exports != 1 {
				t.Fatalf("exports=%d", exports)
			}
			if mode == "allowed" {
				if err != nil || endpoint != "https://selected.invalid" || key != "private-account-key" || mappedDegradationModel("candidate", mapping) != "mapped" {
					t.Fatalf("endpoint=%s err=%v", endpoint, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatalf("unsafe export refusal: %v", err)
			}
		})
	}
}
