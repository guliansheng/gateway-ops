package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/guliansheng/gateway-ops/internal/crypto"
	"github.com/guliansheng/gateway-ops/internal/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestDynamicMonitorProtectionAndDisabledRulesServiceLifecycle(t *testing.T) {
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
	type action struct {
		ID          int64
		Schedulable bool
	}
	var actions []action
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id int64
		if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/admin/accounts/%d/schedulable", &id); err != nil || r.Method != http.MethodPost {
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
		actions = append(actions, action{ID: id, Schedulable: body.Schedulable})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":null}`))
	}))
	defer server.Close()
	cipher, err := crypto.NewCipher("dynamic-protection-lifecycle-test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := cipher.Encrypt("test-key")
	if err != nil {
		t.Fatal(err)
	}
	closedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	deadline := closedAt.Add(10 * time.Minute)
	config := DynamicMonitorConfig{
		Enabled:                     true,
		Groups:                      []DynamicMonitorRuleGroup{ruleGroupWithSingleTimeout(1, 2, 3, 11)},
		ProtectedAccountExternalIDs: []int64{11, 11, 0, -1},
		ErrorGroups: []DynamicMonitorErrorRuleGroup{
			{Name: "first", StatusCodes: []int{502}, Threshold: 1, LookbackMinutes: 10, AllowAutoOpen: true, AutoOpenMinutes: 10},
			{Name: "second", StatusCodes: []int{503}, Threshold: 1, LookbackMinutes: 10, AllowAutoOpen: true, AutoOpenMinutes: 10},
		},
	}
	state := DynamicMonitorState{ClosedAccounts: []DynamicMonitorClosure{
		{AccountExternalID: 12, RuleCategory: "error", RuleGroupName: "first", ErrorStatusCodes: []int{502}, ErrorCount: 2, ErrorWindowMinutes: 10, ClosedAt: closedAt, AutoOpenAt: &deadline},
		{AccountExternalID: 13, RuleCategory: "error", RuleGroupName: "second", ErrorStatusCodes: []int{503}, ErrorCount: 2, ErrorWindowMinutes: 10, ClosedAt: closedAt, AutoOpenAt: &deadline},
	}}
	configJSON, _ := json.Marshal(config)
	stateJSON, _ := json.Marshal(state)
	station := &storage.RelayStation{Name: fmt.Sprintf("dynamic-protection-%d", time.Now().UnixNano()), BaseURL: server.URL, APIKeyCipher: key, DynamicMonitorConfig: string(configJSON), DynamicMonitorState: string(stateJSON)}
	repo := storage.NewRelayStations(db)
	if err := repo.Create(station); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{11, 12, 13} {
		account := storage.RelayAccount{RelayStationID: station.ID, ExternalID: id, Name: fmt.Sprintf("account-%d", id), Status: "active", Schedulable: id == 11, SyncedAt: time.Now(), LatencySamplesJSON: latencyBody(t, 5000, 3, time.Now().UTC())}
		if err := db.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		if err := repo.SetAccountSchedulable(station.ID, id, id == 11); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewService(repo, cipher)
	config = ParseDynamicMonitorConfig(station.DynamicMonitorConfig)
	firstID := config.ErrorGroups[0].ID
	config.ErrorGroups[0].Enabled = dynamicMonitorEnabled(false)
	config.ErrorGroups[0].Name = "renamed"
	if _, err := svc.SaveDynamicMonitorConfig(station.ID, config); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := svc.ApplyDynamicMonitor(ctx, station); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 {
		t.Fatalf("protected account was closed or existing records were reopened immediately: %v", actions)
	}
	if err := svc.ReopenTimedDynamicAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actions, []action{{ID: 13, Schedulable: true}}) {
		t.Fatalf("disabled rule affected another rule's timer: %v", actions)
	}
	stored, err := repo.FindByID(station.ID)
	if err != nil {
		t.Fatal(err)
	}
	remaining := ParseDynamicMonitorState(stored.DynamicMonitorState).ClosedAccounts
	if len(remaining) != 1 || remaining[0].AccountExternalID != 12 || remaining[0].RuleGroupID != firstID || remaining[0].AutoOpenAt == nil || !remaining[0].AutoOpenAt.Equal(deadline) {
		t.Fatalf("disabled rule lost its closure or original deadline: %+v", remaining)
	}
	if err := svc.SetSchedulable(ctx, station.ID, 11, false); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSchedulable(ctx, station.ID, 11, true); err != nil {
		t.Fatal(err)
	}
	config.ErrorGroups[0].Enabled = dynamicMonitorEnabled(true)
	if _, err := svc.SaveDynamicMonitorConfig(station.ID, config); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReopenTimedDynamicAccounts(ctx); err != nil {
		t.Fatal(err)
	}
	want := []action{{ID: 13, Schedulable: true}, {ID: 11, Schedulable: false}, {ID: 11, Schedulable: true}, {ID: 12, Schedulable: true}}
	if !reflect.DeepEqual(actions, want) {
		t.Fatalf("manual scheduling or resumed timer failed: %v", actions)
	}
}
