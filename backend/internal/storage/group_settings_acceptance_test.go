package storage

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Explicitly opted-in tests use a new schema in the supplied disposable database.
// Never connect using the application's environment or remove existing schemas/data.
func groupSettingsAcceptanceDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("GATEWAYOPS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAYOPS_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	address, err := url.Parse(dsn)
	if err != nil || address.Scheme != "postgres" {
		t.Fatal("acceptance tests require an explicit postgres:// disposable database URL")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := base.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	schema := fmt.Sprintf("group_settings_test_%d", time.Now().UnixNano())
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	query.Set("search_path", schema)
	address.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(address.String()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func TestGroupSettingsAcceptancePricingMigrationAndSnapshot(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.Exec("CREATE TABLE relay_groups (id BIGSERIAL PRIMARY KEY, relay_station_id BIGINT NOT NULL, external_id BIGINT NOT NULL, name TEXT NOT NULL, is_exclusive BOOLEAN, rate_multiplier NUMERIC NOT NULL DEFAULT 1, synced_at TIMESTAMPTZ NOT NULL DEFAULT now())").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO relay_groups (relay_station_id, external_id, name, is_exclusive) VALUES (1, 1, 'legacy-public', false), (1, 2, 'legacy-exclusive', true)").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateGroupModelPricing(db); err != nil {
		t.Fatal(err)
	}
	var legacy []struct {
		ExternalID          int64
		ModelPricingEnabled bool
	}
	if err := db.Table("relay_groups").Select("external_id", "model_pricing_enabled").Order("external_id").Find(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 2 || !legacy[0].ModelPricingEnabled || legacy[1].ModelPricingEnabled {
		t.Fatalf("one-time pricing defaults: %+v", legacy)
	}
	if err := db.Exec("UPDATE relay_groups SET model_pricing_enabled = false WHERE external_id = 1").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateGroupModelPricing(db); err != nil {
		t.Fatal(err)
	}
	var enabled bool
	if err := db.Table("relay_groups").Select("model_pricing_enabled").Where("external_id = 1").Scan(&enabled).Error; err != nil || enabled {
		t.Fatalf("migration reset a manual false: %v %v", enabled, err)
	}
	if err := db.AutoMigrate(&RelayStation{}, &RelayGroup{}, &RelayAccount{}, &RelayChannel{}, &RelayChannelGroup{}, &RelayAccountGroup{}, &RelayChannelPricingChange{}); err != nil {
		t.Fatal(err)
	}
	station := RelayStation{ID: 1, Name: "acceptance-station", BaseURL: "https://metadata.invalid"}
	if err := db.Create(&station).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&RelayGroup{}).Where("external_id = 1").Update("monitor_enabled", true).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRelayStations(db)
	groups := []RelayGroup{{ExternalID: 1, Name: "legacy-public", Status: "active"}, {ExternalID: 2, Name: "legacy-exclusive", Status: "active", IsExclusive: true}, {ExternalID: 3, Name: "new-public", Status: "active"}, {ExternalID: 4, Name: "new-exclusive", Status: "active", IsExclusive: true}}
	for i := range groups {
		groups[i].RelayStationID = station.ID
	}
	if err := repo.ReplaceSnapshot(station.ID, groups, nil, nil, nil, nil, "keyword", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.ListGroups(station.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range stored {
		if group.ModelPricingEnabled != (group.ExternalID == 3) {
			t.Fatalf("sync reset/default mismatch: %+v", group)
		}
		if group.ExternalID == 1 && !group.MonitorEnabled {
			t.Fatal("price visibility changed independent monitoring")
		}
	}
}

func TestGroupSettingsAcceptancePromptsRetentionAndScopedClear(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&RelayStation{}, &RelayGroup{}, &GroupDegradationConfig{}, &GroupDegradationResult{}, &DegradationPromptSettings{}); err != nil {
		t.Fatal(err)
	}
	station := RelayStation{Name: "history-acceptance", BaseURL: "https://metadata.invalid"}
	if err := db.Create(&station).Error; err != nil {
		t.Fatal(err)
	}
	groups := []RelayGroup{{RelayStationID: station.ID, ExternalID: 1, Name: "target", MonitorEnabled: true}, {RelayStationID: station.ID, ExternalID: 2, Name: "other", MonitorEnabled: true}}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	repo := NewRelayStations(db)
	ctx := context.Background()
	settings, err := repo.GetDegradationPromptSettings(ctx, station.ID)
	if err != nil || settings.CandyExpectedAnswer != "21" {
		t.Fatalf("default prompts: %+v %v", settings, err)
	}
	var count int64
	if err := db.Model(&DegradationPromptSettings{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("GET created prompt settings: %d %v", count, err)
	}
	settings.CandyPrompt, settings.CandyExpectedAnswer, settings.PelicanPrompt = "custom candy", "42", "custom pelican HTML/SVG"
	if err := repo.SaveDegradationPromptSettings(ctx, station.ID, settings); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetDegradationPromptSettings(ctx, station.ID)
	if err != nil || stored.CandyExpectedAnswer != "42" || stored.PelicanPrompt != settings.PelicanPrompt {
		t.Fatalf("custom prompt storage: %+v %v", stored, err)
	}
	config := DefaultGroupDegradationConfig()
	config.Model, config.HistoryLimit = "candidate", 2
	for _, groupID := range []int64{1, 2} {
		if err := repo.SaveGroupDegradationConfig(ctx, station.ID, groupID, config); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	for _, groupID := range []int64{1, 2} {
		for i := 0; i < 4; i++ {
			result := GroupDegradationResult{StationID: station.ID, GroupExternalID: groupID, Method: "candy", Model: "candidate", JudgeModel: "candidate", Effort: "none", Status: "normal", Reason: "ok", CreatedAt: now.Add(time.Duration(i) * time.Second)}
			if err := db.Create(&result).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := repo.SaveGroupDegradationConfig(ctx, station.ID, 1, config); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&GroupDegradationResult{}).Where("station_id = ? AND group_external_id = ?", station.ID, 1).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("saved limit not enforced: %d %v", count, err)
	}
	until := time.Now().Add(time.Minute)
	if err := db.Model(&GroupDegradationConfig{}).Where("station_id = ? AND group_external_id = ?", station.ID, 1).Updates(map[string]any{"lease_until": until, "lease_token": "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClearGroupDegradationHistory(ctx, station.ID, 1); !errors.Is(err, ErrGroupDegradationBusy) {
		t.Fatalf("active run must block clear: %v", err)
	}
	if err := db.Model(&GroupDegradationConfig{}).Where("station_id = ? AND group_external_id = ?", station.ID, 1).Updates(map[string]any{"lease_until": nil, "lease_token": ""}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ClearGroupDegradationHistory(ctx, station.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&GroupDegradationResult{}).Where("station_id = ? AND group_external_id = ?", station.ID, 1).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("target clear: %d %v", count, err)
	}
	if err := db.Model(&GroupDegradationResult{}).Where("station_id = ? AND group_external_id = ?", station.ID, 2).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("clear affected another group: %d %v", count, err)
	}
	view, err := repo.GroupDegradationView(ctx, station.ID, 1, false)
	if err != nil || view.Config.HistoryLimit != 2 || view.Config.Model != "candidate" {
		t.Fatalf("clear changed configuration: %+v %v", view, err)
	}
}
