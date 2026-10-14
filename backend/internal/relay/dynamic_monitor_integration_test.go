package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Uses an explicitly supplied disposable database, never application credentials.
func TestDynamicMonitorServiceReopenPersistsFreshWindow(t *testing.T) {
	dsn := os.Getenv("GATEWAYOPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAYOPS_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.AutoMigrate(&storage.RelayStation{}, &storage.RelayAccount{}, &storage.RelayGroup{}, &storage.RelayAccountGroup{}, &storage.RelayAccountAdjustmentLog{}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"timer", "minimum", "manual"} {
		t.Run(mode, func(t *testing.T) {
			var actions []bool
			failNext := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/admin/accounts/2276/schedulable" || r.Method != http.MethodPost {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				var body struct {
					Schedulable bool `json:"schedulable"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if failNext {
					failNext = false
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				actions = append(actions, body.Schedulable)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":0,"data":null}`))
			}))
			defer server.Close()
			cipher, err := crypto.NewCipher("dynamic-monitor-integration-test")
			if err != nil {
				t.Fatal(err)
			}
			key, err := cipher.Encrypt("test-key")
			if err != nil {
				t.Fatal(err)
			}
			closedAt := time.Now().UTC().Add(-time.Hour)
			config := DynamicMonitorConfig{Enabled: true, Groups: []DynamicMonitorRuleGroup{ruleGroup(12, 3, 2276)}, AllowAutoOpen: true, AutoOpenMinutes: 10}
			if mode == "minimum" {
				config.AutoOpenMinutes = 0
				config.MinAccountsPerGroup = 1
			}
			configJSON, _ := json.Marshal(config)
			stateJSON, _ := json.Marshal(DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{{AccountExternalID: 2276, ClosedAt: closedAt}}})
			station := &storage.RelayStation{Name: fmt.Sprintf("dynamic-test-%s-%d", mode, time.Now().UnixNano()), BaseURL: server.URL, APIKeyCipher: key, DynamicMonitorConfig: string(configJSON), DynamicMonitorState: string(stateJSON)}
			repo := storage.NewRelayStations(db)
			if err := repo.Create(station); err != nil {
				t.Fatal(err)
			}
			account := storage.RelayAccount{RelayStationID: station.ID, ExternalID: 2276, Name: "test", Status: "active", Schedulable: false, SyncedAt: time.Now(), LatencySamplesJSON: latencyBody(t, 20000, 3, closedAt.Add(-time.Hour))}
			if err := db.Create(&account).Error; err != nil {
				t.Fatal(err)
			}
			if err := repo.SetAccountSchedulable(station.ID, 2276, false); err != nil {
				t.Fatal(err)
			}
			group := storage.RelayGroup{RelayStationID: station.ID, ExternalID: 1, Name: "test", Status: "active", SyncedAt: time.Now()}
			if err := db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&storage.RelayAccountGroup{RelayAccountID: account.ID, RelayGroupID: group.ID}).Error; err != nil {
				t.Fatal(err)
			}
			svc := NewService(repo, cipher)
			ctx := context.Background()
			reopen := func() error {
				switch mode {
				case "timer":
					return svc.ReopenTimedDynamicAccounts(ctx)
				case "minimum":
					return svc.ApplyDynamicMonitor(ctx, station)
				default:
					return svc.SetSchedulable(ctx, station.ID, 2276, true)
				}
			}
			if err := reopen(); err == nil {
				t.Fatal("expected failed remote reopen")
			}
			stored, err := repo.FindByID(station.ID)
			if err != nil {
				t.Fatal(err)
			}
			failedState := ParseDynamicMonitorState(stored.DynamicMonitorState)
			if len(failedState.ClosedAccounts) != 1 || failedState.Judged[2276].After(closedAt) {
				t.Fatalf("failed reopen consumed state: %+v", failedState)
			}
			beforeOpen := time.Now().UTC()
			if err := reopen(); err != nil {
				t.Fatal(err)
			}
			stored, err = repo.FindByID(station.ID)
			if err != nil {
				t.Fatal(err)
			}
			state := ParseDynamicMonitorState(stored.DynamicMonitorState)
			if len(state.ClosedAccounts) != 0 || state.Judged[2276].Before(beforeOpen) {
				t.Fatalf("reopen did not persist fresh boundary: %+v", state)
			}
			// A fresh service and the stale caller station must reload the saved boundary.
			svc = NewService(repo, cipher)
			for i := 0; i < 3; i++ {
				if err := svc.ApplyDynamicMonitor(ctx, station); err != nil {
					t.Fatal(err)
				}
			}
			if len(actions) != 1 || !actions[0] {
				t.Fatalf("old samples reclosed account: %v", actions)
			}
			samples := parseLatencySamples(account.LatencySamplesJSON)
			for i := 1; i <= 3; i++ {
				samples = append(samples, storage.RelayLatencySample{FirstTokenMS: 20000, CreatedAt: state.Judged[2276].Add(time.Duration(i) * time.Second)})
				if err := db.Model(&storage.RelayAccount{}).Where("id = ?", account.ID).Update("latency_samples_json", mustLatencyJSON(t, samples)).Error; err != nil {
					t.Fatal(err)
				}
				if err := svc.ApplyDynamicMonitor(ctx, station); err != nil {
					t.Fatal(err)
				}
				want := 1
				if i == 3 {
					want = 2
				}
				if len(actions) != want {
					t.Fatalf("after %d new calls: actions = %v", i, actions)
				}
			}
			if actions[1] {
				t.Fatalf("three fresh slow calls did not close account: %v", actions)
			}
		})
	}
}
