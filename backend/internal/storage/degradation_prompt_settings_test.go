package storage

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestDegradationPromptSettingsDefaultsAndAdminJSON(t *testing.T) {
	settings := DefaultDegradationPromptSettings()
	if settings.CandyExpectedAnswer != "21" || settings.CandyPrompt != DefaultCandyDegradationPrompt || settings.PelicanPrompt != DefaultPelicanDegradationPrompt || settings.PelicanReference != DefaultPelicanDegradationReference {
		t.Fatalf("defaults=%+v", settings)
	}
	for _, fragment := range []string{"黑色的袋子", "圆形 | 7 | 9 | 8", "五角星形 | 7 | 6 | 4"} {
		if !strings.Contains(settings.CandyPrompt, fragment) {
			t.Errorf("default candy prompt missing %q", fragment)
		}
	}
	if !strings.Contains(settings.PelicanPrompt, "鹈鹕骑自行车") || !strings.Contains(settings.PelicanPrompt, "prefers-reduced-motion") {
		t.Fatal("original pelican default was changed")
	}
	settings.ID, settings.StationID = 123, 456
	settings.CreatedAt, settings.UpdatedAt = time.Now(), time.Now()
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	if len(fields) != 4 || fields["candy_prompt"] == nil || fields["candy_expected_answer"] == nil || fields["pelican_prompt"] == nil || fields["pelican_reference"] == nil {
		t.Fatalf("admin JSON=%s", data)
	}
	s, err := schema.Parse(&DegradationPromptSettings{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil || len(s.Relationships.Relations) != 0 || s.Table != "degradation_prompt_settings" || s.LookUpField("StationID") == nil {
		t.Fatalf("settings depend on snapshot schema: %v %+v", err, s)
	}
	indexes := s.ParseIndexes()
	if index, ok := indexes["idx_degradation_prompt_station"]; !ok || index.Class != "UNIQUE" || len(index.Fields) != 1 || index.Fields[0].DBName != "station_id" {
		t.Fatalf("station-owned unique index missing: %+v", indexes)
	}
}

func TestDegradationPromptSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*DegradationPromptSettings)
	}{
		{"empty candy", func(s *DegradationPromptSettings) { s.CandyPrompt = " \n\t " }},
		{"empty answer", func(s *DegradationPromptSettings) { s.CandyExpectedAnswer = "" }},
		{"empty pelican", func(s *DegradationPromptSettings) { s.PelicanPrompt = "" }},
		{"large candy", func(s *DegradationPromptSettings) { s.CandyPrompt = strings.Repeat("a", 32*1024+1) }},
		{"large answer", func(s *DegradationPromptSettings) { s.CandyExpectedAnswer = strings.Repeat("a", 4*1024+1) }},
		{"large pelican bytes", func(s *DegradationPromptSettings) { s.PelicanPrompt = strings.Repeat("中", 11000) }},
		{"large reference", func(s *DegradationPromptSettings) { s.PelicanReference = strings.Repeat("a", 8*1024+1) }},
		{"invalid utf8", func(s *DegradationPromptSettings) { s.CandyPrompt = string([]byte{0xff}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings := DefaultDegradationPromptSettings()
			tc.mutate(&settings)
			if _, err := NormalizeDegradationPromptSettings(settings); !errors.Is(err, ErrInvalidDegradationPromptSettings) {
				t.Fatalf("accepted invalid settings: %v", err)
			}
		})
	}
	for _, control := range []string{"\x00", "\r", "\v", "\f", "\x1f", "\x7f", "\u0085"} {
		for _, field := range []string{"candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference"} {
			t.Run(field+"/"+control, func(t *testing.T) {
				settings := DefaultDegradationPromptSettings()
				switch field {
				case "candy_prompt":
					settings.CandyPrompt = control + "prompt"
				case "candy_expected_answer":
					settings.CandyExpectedAnswer = "answer" + control
				case "pelican_prompt":
					settings.PelicanPrompt = "prompt" + control + "body"
				case "pelican_reference":
					settings.PelicanReference = "checklist" + control + "body"
				}
				if _, err := NormalizeDegradationPromptSettings(settings); !errors.Is(err, ErrInvalidDegradationPromptSettings) {
					t.Fatalf("accepted control %q in %s", control, field)
				}
			})
		}
	}
	settings := DegradationPromptSettings{CandyPrompt: " \n\tcustom\n\tquestion \n", CandyExpectedAnswer: "\t Forty-two \n", PelicanPrompt: " \t draw a tree \n ", PelicanReference: " \t review checklist \n "}
	got, err := NormalizeDegradationPromptSettings(settings)
	if err != nil || got.CandyPrompt != "custom\n\tquestion" || got.CandyExpectedAnswer != "Forty-two" || got.PelicanPrompt != "draw a tree" || got.PelicanReference != "review checklist" {
		t.Fatalf("trimmed=%+v err=%v", got, err)
	}
	settings = DegradationPromptSettings{CandyPrompt: strings.Repeat("a", 32*1024), CandyExpectedAnswer: strings.Repeat("b", 4*1024), PelicanPrompt: strings.Repeat("c", 32*1024), PelicanReference: strings.Repeat("d", 8*1024)}
	if _, err := NormalizeDegradationPromptSettings(settings); err != nil {
		t.Fatalf("exact byte limits rejected: %v", err)
	}
}

func degradationPromptStationStep(t *testing.T, stationID uint) degradationSQLStep {
	t.Helper()
	return degradationSQLStep{contains: `FROM "relay_stations"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(stationID)}}, check: func(query string, args []driver.NamedValue) {
		if !strings.HasPrefix(query, `SELECT "id"`) || !strings.Contains(query, `"relay_stations"."deleted_at" IS NULL`) || len(args) != 2 || args[0].Value != int64(stationID) {
			t.Errorf("station validation SQL=%s args=%+v", query, args)
		}
	}}
}

func TestDegradationPromptSettingsGetIsReadOnlyAndScoped(t *testing.T) {
	for _, stored := range []bool{false, true} {
		t.Run(map[bool]string{false: "logical defaults", true: "stored settings"}[stored], func(t *testing.T) {
			lookup := degradationSQLStep{contains: `FROM "degradation_prompt_settings"`, columns: []string{"id", "station_id", "candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference"}, check: func(query string, args []driver.NamedValue) {
				if !strings.Contains(query, "station_id = $1") || len(args) != 2 || args[0].Value != int64(12) {
					t.Errorf("unscoped lookup: %s %+v", query, args)
				}
			}}
			if stored {
				lookup.rows = [][]driver.Value{{int64(44), int64(12), " custom candy \n", " 42 ", " custom pelican ", " custom checklist "}}
			}
			repo, state := newDegradationSQL(t, degradationPromptStationStep(t, 12), lookup)
			settings, err := repo.GetDegradationPromptSettings(context.Background(), 12)
			if err != nil || state.commits != 0 || state.rollbacks != 0 {
				t.Fatalf("read wrote data: settings=%+v err=%v state=%+v", settings, err, state)
			}
			if stored {
				if settings.CandyPrompt != "custom candy" || settings.CandyExpectedAnswer != "42" || settings.PelicanPrompt != "custom pelican" || settings.PelicanReference != "custom checklist" {
					t.Fatalf("stored settings lost: %+v", settings)
				}
			} else if !reflect.DeepEqual(settings, DefaultDegradationPromptSettings()) {
				t.Fatalf("logical defaults=%+v", settings)
			}
		})
	}
}

func TestDegradationPromptSettingsGetErrorsDoNotDefault(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		missingStation, invalidStored bool
	}{
		{name: "missing station", missingStation: true}, {name: "database error"}, {name: "invalid stored row", invalidStored: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			station := degradationPromptStationStep(t, 12)
			steps := []degradationSQLStep{station}
			if tc.missingStation {
				steps[0].rows = nil
			} else {
				lookup := degradationSQLStep{contains: `FROM "degradation_prompt_settings"`, columns: []string{"candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference"}, err: errors.New("database secret details")}
				if tc.invalidStored {
					lookup.err = nil
					lookup.rows = [][]driver.Value{{"", "42", "tree", "checklist"}}
				}
				steps = append(steps, lookup)
			}
			repo, _ := newDegradationSQL(t, steps...)
			_, err := repo.GetDegradationPromptSettings(context.Background(), 12)
			if err == nil || tc.missingStation && !errors.Is(err, gorm.ErrRecordNotFound) || tc.invalidStored && !errors.Is(err, ErrInvalidDegradationPromptSettings) {
				t.Fatalf("lookup incorrectly defaulted: %v", err)
			}
		})
	}
}

func TestDegradationPromptSettingsSaveUpsertIsStationOwned(t *testing.T) {
	settings := DegradationPromptSettings{ID: 99, StationID: 999, CandyPrompt: " candy ", CandyExpectedAnswer: " 42 ", PelicanPrompt: " tree ", PelicanReference: " checklist ", CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(2, 0)}
	upsert := degradationSQLStep{contains: `INSERT INTO "degradation_prompt_settings"`, columns: []string{"id"}, rows: [][]driver.Value{{int64(44)}}, check: func(query string, args []driver.NamedValue) {
		if !strings.Contains(query, `ON CONFLICT ("station_id") DO UPDATE SET`) || strings.Contains(query, `"id","station_id"`) || len(args) != 7 {
			t.Fatalf("bad upsert: %s %+v", query, args)
		}
		want := []driver.Value{int64(12), "candy", "42", "tree", "checklist"}
		for i := range want {
			if args[i].Value != want[i] {
				t.Errorf("upsert arg %d=%v want=%v", i, args[i].Value, want[i])
			}
		}
		for _, field := range []string{"candy_prompt", "candy_expected_answer", "pelican_prompt", "pelican_reference", "updated_at"} {
			if !strings.Contains(query, `"`+field+`"="excluded"."`+field+`"`) {
				t.Errorf("upsert missing update %s: %s", field, query)
			}
		}
		for _, arg := range args[5:] {
			if value, ok := arg.Value.(time.Time); !ok || value.Year() < 2020 {
				t.Errorf("client timestamp trusted: %v", arg)
			}
		}
	}}
	repo, _ := newDegradationSQL(t, degradationPromptStationStep(t, 12), upsert)
	if err := repo.SaveDegradationPromptSettings(context.Background(), 12, settings); err != nil {
		t.Fatal(err)
	}
}

func TestDegradationPromptSettingsSaveRejectsInvalidOrMissingStation(t *testing.T) {
	repo, _ := newDegradationSQL(t)
	if err := repo.SaveDegradationPromptSettings(context.Background(), 12, DegradationPromptSettings{}); !errors.Is(err, ErrInvalidDegradationPromptSettings) {
		t.Fatalf("invalid settings wrote data: %v", err)
	}
	station := degradationPromptStationStep(t, 12)
	station.rows = nil
	repo, _ = newDegradationSQL(t, station)
	if err := repo.SaveDegradationPromptSettings(context.Background(), 12, DefaultDegradationPromptSettings()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("settings created for missing station: %v", err)
	}
}
