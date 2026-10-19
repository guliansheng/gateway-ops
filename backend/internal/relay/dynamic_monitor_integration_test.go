package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
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

// Verify the real snapshot/clone paths, not just the in-memory assignment plan.
// This database must be disposable, as in the reopen integration test above.
func TestDynamicMonitorAccountAssignmentPersistsAcrossSnapshots(t *testing.T) {
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
	if err := db.AutoMigrate(
		&storage.RelayStation{}, &storage.RelayAccount{}, &storage.RelayGroup{},
		&storage.RelayAccountGroup{}, &storage.RelayChannel{}, &storage.RelayChannelGroup{},
		&storage.RelayChannelPricingChange{}, &storage.RelayAccountAdjustmentLog{},
	); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name          string
		batchClone    bool
		coldStart     bool
		delayedGroups bool
		schedulable   bool
	}{
		{name: "new_account", schedulable: true},
		{name: "upstream_clone"},
		{name: "delayed_group_links", delayedGroups: true},
		{name: "batch_clone", batchClone: true},
		{name: "batch_clone_before_baseline", batchClone: true, coldStart: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var remoteMu sync.Mutex
			remoteAccounts := []remoteAccount{
				{ID: 11, Name: "source", Type: "apikey", Status: "active", GroupIDs: []int64{101, 202}},
				{ID: 12, Name: "existing", Type: "apikey", Status: "active", GroupIDs: []int64{202}},
				{ID: 13, Name: "excluded", Type: "apikey", Status: "active", GroupIDs: []int64{202}},
			}
			newAccount := remoteAccount{ID: 21, Name: "new", Type: "apikey", Status: "active", Schedulable: test.schedulable, GroupIDs: []int64{101, 202}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				remoteMu.Lock()
				defer remoteMu.Unlock()
				if r.Header.Get("x-api-key") != "test-key" {
					t.Error("missing test admin key")
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				var data any
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/admin/groups/all":
					data = []remoteGroup{
						{ID: 101, Name: "low", Status: "active", RateMultiplier: 1},
						{ID: 202, Name: "high", Status: "active", RateMultiplier: 4},
					}
				case "GET /api/v1/admin/accounts":
					data = map[string]any{"items": remoteAccounts, "pages": 1}
				case "GET /api/v1/admin/channels", "GET /api/v1/admin/users", "GET /api/v1/admin/usage":
					data = map[string]any{"items": []any{}, "pages": 1}
				case "GET /api/v1/admin/usage/stats", "GET /api/v1/admin/ops/request-errors":
					data = map[string]any{"total": 0}
				case "POST /api/v1/admin/accounts/11/duplicate":
					remoteAccounts = append(remoteAccounts, newAccount)
					data = newAccount
				case "PUT /api/v1/admin/accounts/21":
					var update remoteAccount
					if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					for index := range remoteAccounts {
						if remoteAccounts[index].ID == 21 {
							remoteAccounts[index].Name = update.Name
							remoteAccounts[index].Status = update.Status
							remoteAccounts[index].Schedulable = update.Schedulable
						}
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			cipher, err := crypto.NewCipher("dynamic-monitor-assignment-test")
			if err != nil {
				t.Fatal(err)
			}
			key, err := cipher.Encrypt("test-key")
			if err != nil {
				t.Fatal(err)
			}
			config := DynamicMonitorConfig{
				Groups:          []DynamicMonitorRuleGroup{ruleGroup(30, 5, 11), ruleGroup(60, 5, 12)},
				GroupRuleGroups: map[string]int{"101": 0, "202": 1},
			}
			configJSON, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			station := &storage.RelayStation{
				Name:                 fmt.Sprintf("assignment-%s-%d", test.name, time.Now().UnixNano()),
				BaseURL:              server.URL,
				APIKeyCipher:         key,
				DynamicMonitorConfig: string(configJSON),
			}
			repo := storage.NewRelayStations(db)
			if err := repo.Create(station); err != nil {
				t.Fatal(err)
			}
			svc := NewService(repo, cipher)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			// Seed the local snapshot before enabling monitoring, as the UI does.
			if err := svc.SyncSnapshot(ctx, station.ID); err != nil {
				t.Fatal(err)
			}
			config.Enabled = true
			if _, err := svc.SaveDynamicMonitorConfig(station.ID, config); err != nil {
				t.Fatal(err)
			}
			if !test.coldStart {
				if err := svc.ApplyDynamicMonitor(ctx, station); err != nil {
					t.Fatal(err)
				}
			}

			if test.batchClone {
				result, err := svc.BatchCloneAccounts(ctx, station.ID, BatchCloneInput{Groups: []BatchCloneGroup{{
					SourceAccountExternalID: 11,
					Accounts:                []BatchCloneAccount{{Name: "clone", APIKey: "test-account-key"}},
				}}})
				if err != nil || result.SyncError != "" || result.Succeeded != 1 || result.Failed != 0 {
					t.Fatalf("clone failed: result=%+v err=%v", result, err)
				}
			} else {
				remoteMu.Lock()
				if test.delayedGroups {
					newAccount.GroupIDs = nil
				}
				remoteAccounts = append(remoteAccounts, newAccount)
				remoteMu.Unlock()
				if err := svc.SyncSnapshot(ctx, station.ID); err != nil {
					t.Fatal(err)
				}
				if test.delayedGroups {
					stored, err := repo.FindByID(station.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, seen := ParseDynamicMonitorState(stored.DynamicMonitorState).SeenAccounts[21]; seen {
						t.Fatal("account without group links was prematurely marked seen")
					}
					remoteMu.Lock()
					remoteAccounts[len(remoteAccounts)-1].GroupIDs = []int64{101, 202}
					remoteMu.Unlock()
					if err := svc.SyncSnapshot(ctx, station.ID); err != nil {
						t.Fatal(err)
					}
				}
			}

			for round := 0; round < 3; round++ {
				if round > 0 {
					// A fresh service must reload persisted membership, with no duplicate IDs.
					svc = NewService(repo, cipher)
					if err := svc.SyncSnapshot(ctx, station.ID); err != nil {
						t.Fatal(err)
					}
				}
				stored, err := repo.FindByID(station.ID)
				if err != nil {
					t.Fatal(err)
				}
				var saved DynamicMonitorConfig
				if err := json.Unmarshal([]byte(stored.DynamicMonitorConfig), &saved); err != nil {
					t.Fatal(err)
				}
				if len(saved.Groups) != 2 || len(saved.Groups[0].AccountExternalIDs) != 1 || saved.Groups[0].AccountExternalIDs[0] != 11 {
					t.Fatalf("round %d: existing rule membership changed: %+v", round, saved.Groups)
				}
				if ids := saved.Groups[1].AccountExternalIDs; len(ids) != 2 || !containsID(ids, 12) || !containsID(ids, 21) {
					t.Fatalf("round %d: new account was not persisted in the higher-rate rule group: %v", round, ids)
				}
				if _, seen := ParseDynamicMonitorState(stored.DynamicMonitorState).SeenAccounts[21]; !seen {
					t.Fatalf("round %d: assigned account was not marked seen", round)
				}
			}

			// A user removing an already assigned account must still be respected.
			if _, err := svc.SaveDynamicMonitorConfig(station.ID, config); err != nil {
				t.Fatal(err)
			}
			if err := svc.SyncSnapshot(ctx, station.ID); err != nil {
				t.Fatal(err)
			}
			stored, err := repo.FindByID(station.ID)
			if err != nil {
				t.Fatal(err)
			}
			if ids := ParseDynamicMonitorConfig(stored.DynamicMonitorConfig).Groups[1].AccountExternalIDs; containsID(ids, 21) {
				t.Fatalf("manually removed account was re-added: %v", ids)
			}
		})
	}
}
