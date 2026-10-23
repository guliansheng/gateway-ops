package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDegradationAcceptanceCombinedAndAccountHistory(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&RelayStation{}, &RelayGroup{}, &RelayAccount{}, &GroupDegradationConfig{}, &GroupDegradationResult{}, &AccountDegradationRun{}, &AccountDegradationResult{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&GroupDegradationConfig{}, &GroupDegradationResult{}, &AccountDegradationRun{}, &AccountDegradationResult{}); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	station := RelayStation{Name: "degradation-acceptance", BaseURL: "https://metadata.invalid"}
	if err := db.Create(&station).Error; err != nil {
		t.Fatal(err)
	}
	group := RelayGroup{RelayStationID: station.ID, ExternalID: 9, Name: "test-group", Status: "active", SyncedAt: time.Now()}
	account := RelayAccount{RelayStationID: station.ID, ExternalID: 11, Name: "test-account", SyncedAt: time.Now()}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRelayStations(db)
	ctx := context.Background()
	config := DefaultGroupDegradationConfig()
	config.Enabled = true
	config.Model = "mock"
	config.PelicanIntervalMinutes = 60
	config.CandyIntervalMinutes = 10
	if err := repo.SaveGroupDegradationConfig(ctx, station.ID, 9, config); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimDueGroupDegradations(ctx, 2, 9*time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].Method != "pelican" {
		t.Fatalf("first claim=%+v err=%v", claimed, err)
	}
	if _, err := repo.ClaimGroupDegradation(ctx, station.ID, 9, 9*time.Minute); !errors.Is(err, ErrGroupDegradationBusy) {
		t.Fatalf("lease overlap accepted: %v", err)
	}
	result := GroupDegradationResult{Method: "pelican", Model: "mock", JudgeModel: "mock", Effort: "none", Status: "error", Reason: "simulated timeout"}
	if err := repo.CompleteGroupDegradation(ctx, claimed[0], &result); err != nil {
		t.Fatal(err)
	}
	var stored GroupDegradationConfig
	if err := db.Where("station_id = ?", station.ID).First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.PelicanNextRunAt == nil || stored.CandyNextRunAt == nil || stored.PelicanNextRunAt.Sub(*stored.CandyNextRunAt) != 50*time.Minute {
		t.Fatalf("independent deadlines: %+v", stored)
	}
	beforePelican := *stored.PelicanNextRunAt
	past := time.Now().Add(-time.Minute)
	if err := db.Model(&stored).Updates(map[string]any{"candy_next_run_at": past, "next_run_at": past}).Error; err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.ClaimDueGroupDegradations(ctx, 2, 9*time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].Method != "candy" || !claimed[0].PelicanNextRunAt.Equal(beforePelican) {
		t.Fatalf("candy clock touched pelican: %+v err=%v", claimed, err)
	}
	result = GroupDegradationResult{Method: "candy", Model: "mock", JudgeModel: "mock", Effort: "none", Status: "normal", Reason: "simulated pass"}
	if err := repo.CompleteGroupDegradation(ctx, claimed[0], &result); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimAccountDegradation(ctx, station.ID, 11, 9*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClaimAccountDegradation(ctx, station.ID, 11, 9*time.Minute); !errors.Is(err, ErrGroupDegradationBusy) {
		t.Fatalf("parallel account test accepted: %v", err)
	}
	manual := AccountDegradationResult{GroupDegradationResult: GroupDegradationResult{Method: "candy", Model: "mock", JudgeModel: "mock", Effort: "none", Status: "error", Reason: "simulated 403"}}
	if err := repo.CompleteAccountDegradation(ctx, lease, &manual); err != nil {
		t.Fatal(err)
	}
	history, err := repo.AccountDegradationHistory(ctx, station.ID, 11)
	if err != nil || len(history) != 1 || history[0].Reason != "simulated 403" {
		t.Fatalf("manual history=%+v err=%v", history, err)
	}
	view, err := repo.GroupDegradationView(ctx, station.ID, 9, false)
	if err != nil || len(view.Results) != 2 {
		t.Fatalf("manual leaked into group: %+v err=%v", view, err)
	}
}
