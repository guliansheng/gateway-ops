package storage

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"
)

// These tests use only the explicitly opted-in disposable PostgreSQL database
// and isolated schema provided by the shared acceptance-test helper.
func TestChannelConsumptionTotalsAcceptanceRecharges(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &BalanceSnapshot{}, &BalanceChangeLog{}, &ChannelDailyBalance{}, &OperationLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)

	for i, tc := range []struct {
		name       string
		balances   []float64
		recharges  []float64
		multiplier float64
		manual     bool
		want       float64
	}{
		{name: "no recharge", balances: []float64{100, 80}, want: 20},
		{name: "recharge consumed to zero", balances: []float64{100, 150, 0}, recharges: []float64{50}, want: 150},
		{name: "opening and closing zero", balances: []float64{0, 50, 0}, recharges: []float64{50}, want: 50},
		{name: "zero opening with partial consumption", balances: []float64{0, 50, 40}, recharges: []float64{50}, want: 10},
		{name: "separately sampled recharge", balances: []float64{100, 20, 70, 60}, recharges: []float64{50}, want: 90},
		{name: "recharge hidden by net decrease", balances: []float64{100, 60}, recharges: []float64{50}, want: 90},
		{name: "recharge hidden by unchanged balance", balances: []float64{100, 100}, recharges: []float64{50}, want: 50},
		{name: "recharge hidden by net increase", balances: []float64{100, 130}, recharges: []float64{50}, want: 20},
		{name: "recharge alone is not consumption", balances: []float64{100, 150}, recharges: []float64{50}, want: 0},
		{name: "discounted recharge", balances: []float64{100, 60}, recharges: []float64{5}, multiplier: 0.1, want: 90},
		{name: "premium recharge", balances: []float64{100, 60}, recharges: []float64{100}, multiplier: 2, want: 90},
		{name: "multiple recharges", balances: []float64{100, 60}, recharges: []float64{20, 30}, want: 90},
		{name: "manual balances already use cash", balances: []float64{100, 60}, recharges: []float64{50}, multiplier: 0.1, manual: true, want: 90},
		{name: "sampling reset with recharge", balances: []float64{100, 0, 95, 145}, recharges: []float64{50}, want: 5},
		{name: "no reliable balance", recharges: []float64{50}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			multiplier := tc.multiplier
			if multiplier == 0 {
				multiplier = 1
			}
			channel := Channel{Name: fmt.Sprintf("recharge-test-%d", i), Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: multiplier}
			if tc.manual {
				channel.BalanceMode = BalanceModeManual
			}
			if err := db.Create(&channel).Error; err != nil {
				t.Fatal(err)
			}
			for j, balance := range tc.balances {
				at := since.Add(time.Duration(j*2) * time.Hour)
				if j == 0 {
					at = since.Add(-time.Minute)
				}
				if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: balance, SampledAt: at}); err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&channel).Updates(map[string]any{"last_balance": balance, "last_balance_at": at}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, amount := range tc.recharges {
				entry := OperationLedgerEntry{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: amount, OccurredAt: since.Add(time.Hour)}
				if err := db.Create(&entry).Error; err != nil {
					t.Fatal(err)
				}
			}
			totals, err := rates.channelConsumptionTotals(since, since.Add(12*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if got := totals[channel.ID]; math.Abs(got-tc.want) > 1e-8 {
				t.Fatalf("consumption = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChannelConsumptionTotalsAcceptanceRechargeScope(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &BalanceSnapshot{}, &BalanceChangeLog{}, &ChannelDailyBalance{}, &OperationLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	operations := NewOperations(db)
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)
	channel := Channel{Name: "recharge-scope", Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: 1}
	other := Channel{Name: "other-channel", Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: 1}
	for _, item := range []*Channel{&channel, &other} {
		if err := db.Create(item).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, snapshot := range []BalanceSnapshot{
		{ChannelID: channel.ID, Balance: 100, SampledAt: since.Add(-time.Minute)},
		{ChannelID: channel.ID, Balance: 60, SampledAt: since.Add(4 * time.Hour)},
	} {
		if err := rates.AppendBalance(&snapshot); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&channel).Updates(map[string]any{"last_balance": 60, "last_balance_at": since.Add(4 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	// All these records must be excluded: wrong channel, missing channel,
	// wrong direction/category, before the range, or after the latest sample.
	for _, entry := range []OperationLedgerEntry{
		{ChannelID: &other.ID, Direction: "expense", Category: "upstream_recharge", Amount: 100, OccurredAt: since.Add(time.Hour)},
		{Direction: "expense", Category: "upstream_recharge", Amount: 100, OccurredAt: since.Add(time.Hour)},
		{ChannelID: &channel.ID, Direction: "income", Category: "upstream_recharge", Amount: 100, OccurredAt: since.Add(time.Hour)},
		{ChannelID: &channel.ID, Direction: "expense", Category: "operating_expense", Amount: 100, OccurredAt: since.Add(time.Hour)},
		{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 100, OccurredAt: since.Add(-time.Second)},
		{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 100, OccurredAt: since.Add(5 * time.Hour)},
		{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 100, OccurredAt: since.Add(24 * time.Hour)},
	} {
		if err := db.Create(&entry).Error; err != nil {
			t.Fatal(err)
		}
	}
	assertTotal := func(want float64) {
		t.Helper()
		totals, err := rates.channelConsumptionTotals(since, since.Add(12*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if got := totals[channel.ID]; math.Abs(got-want) > 1e-8 {
			t.Fatalf("consumption = %v, want %v", got, want)
		}
	}
	assertTotal(40)
	entry := OperationLedgerEntry{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 50, OccurredAt: since}
	if err := operations.CreateLedger(&entry); err != nil {
		t.Fatal(err)
	}
	assertTotal(90)
	entry.Amount = 20
	if err := operations.UpdateLedger(&entry); err != nil {
		t.Fatal(err)
	}
	assertTotal(60)
	if err := operations.DeleteLedger(entry.ID); err != nil {
		t.Fatal(err)
	}
	assertTotal(40)
}

// A daily row written after midnight (late cron or startup recovery) must
// still act as the opening baseline for today, even after the pre-midnight
// balance snapshots have been cleaned up. Otherwise consumption and recharges
// between midnight and the first intraday sample are silently lost.
func TestChannelConsumptionTotalsAcceptanceLateDailyBaseline(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &BalanceSnapshot{}, &BalanceChangeLog{}, &ChannelDailyBalance{}, &OperationLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)
	channel := Channel{Name: "late-daily", Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: 1}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	// Pre-midnight snapshot exists only for seeding the daily row.
	if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: 100, SampledAt: since.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := rates.CaptureDailyBalances(since.Add(3 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Snapshot cleanup removes the pre-midnight sample.
	if err := db.Where("channel_id = ?", channel.ID).Delete(&BalanceSnapshot{}).Error; err != nil {
		t.Fatal(err)
	}
	// Midnight to first sample: 30 consumed, then a 50 recharge; first
	// intraday sample already includes the recharge.
	if err := db.Create(&OperationLedgerEntry{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 50, OccurredAt: since.Add(time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: 120, SampledAt: since.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	totals, err := rates.channelConsumptionTotals(since, since.Add(12*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// 100 (daily baseline) + 50 recharge - 120 closing = 30 consumed.
	if got := totals[channel.ID]; math.Abs(got-30) > 1e-8 {
		t.Fatalf("consumption = %v, want 30", got)
	}
}

// A daily row derived from the first intraday sample (no pre-midnight snapshot)
// is not a midnight baseline: it already contains any recharge and consumption
// before that sample. Using it as a trusted opening would double count.
func TestChannelConsumptionTotalsAcceptanceUnreliableDailyBaseline(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &BalanceSnapshot{}, &BalanceChangeLog{}, &ChannelDailyBalance{}, &OperationLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)
	channel := Channel{Name: "unreliable-daily", Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: 1}
	if err := db.Create(&channel).Error; err != nil {
		t.Fatal(err)
	}
	// No pre-midnight snapshot: CaptureDailyBalances derives the daily row
	// from the first intraday sample, which must not be marked reliable.
	if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: 50, SampledAt: since.Add(30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := rates.CaptureDailyBalances(since.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The recharge happened before the first sample and is already inside 50.
	if err := db.Create(&OperationLedgerEntry{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 50, OccurredAt: since.Add(10 * time.Minute)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: 40, SampledAt: since.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	totals, err := rates.channelConsumptionTotals(since, since.Add(12*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Without a trusted opening, the recharge cannot be reconciled; only the
	// observed net decrease (50 -> 40) counts, never a double count.
	if got := totals[channel.ID]; math.Abs(got-10) > 1e-8 {
		t.Fatalf("consumption = %v, want 10", got)
	}
}

func TestChannelConsumptionTotalsAcceptanceTimeBounds(t *testing.T) {
	db := groupSettingsAcceptanceDB(t)
	if err := db.AutoMigrate(&Channel{}, &BalanceSnapshot{}, &BalanceChangeLog{}, &ChannelDailyBalance{}, &OperationLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	rates := NewRates(db)
	day := time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)
	type point struct {
		at     time.Duration
		amount float64
	}
	for _, tc := range []struct {
		name      string
		startHour int
		all       bool
		samples   []point
		recharges []point
		want      float64
	}{
		{
			name: "rolling range ignores midnight daily balance", startHour: 12,
			samples:   []point{{-12 * time.Hour, 200}, {-time.Minute, 100}, {time.Hour, 60}},
			recharges: []point{{30 * time.Minute, 50}}, want: 90,
		},
		{
			name:      "cutoff excludes future snapshots and pending recharges",
			samples:   []point{{-time.Minute, 100}, {time.Hour, 80}, {4 * time.Hour, 160}},
			recharges: []point{{30 * time.Minute, 30}, {90 * time.Minute, 50}, {3 * time.Hour, 50}}, want: 50,
		},
		{
			name:      "recharge at ending sample is included",
			samples:   []point{{-time.Minute, 100}, {2 * time.Hour, 120}},
			recharges: []point{{2 * time.Hour, 50}}, want: 30,
		},
		{
			name:      "first sample already contains same-time recharge",
			samples:   []point{{0, 100}, {time.Hour, 70}},
			recharges: []point{{0, 100}, {30 * time.Minute, 20}}, want: 50,
		},
		{
			name: "all-time starts after initial funding", all: true,
			samples:   []point{{0, 100}, {time.Hour, 70}},
			recharges: []point{{-time.Minute, 200}, {0, 100}, {30 * time.Minute, 20}}, want: 50,
		},
		{
			name:      "untrusted opening retains observed spending",
			samples:   []point{{10 * time.Minute, 100}, {20 * time.Minute, 20}, {40 * time.Minute, 60}},
			recharges: []point{{30 * time.Minute, 50}}, want: 80,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			since := day.Add(time.Duration(tc.startHour) * time.Hour)
			channel := Channel{Name: tc.name, Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: 1}
			if err := db.Create(&channel).Error; err != nil {
				t.Fatal(err)
			}
			for _, p := range tc.samples {
				at := since.Add(p.at)
				if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: p.amount, SampledAt: at}); err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&channel).Updates(map[string]any{"last_balance": p.amount, "last_balance_at": at}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, p := range tc.recharges {
				if err := db.Create(&OperationLedgerEntry{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: p.amount, OccurredAt: since.Add(p.at)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			until := since.Add(2 * time.Hour)
			if tc.all {
				since = time.Time{}
			}
			totals, err := rates.channelConsumptionTotals(since, until)
			if err != nil {
				t.Fatal(err)
			}
			if got := totals[channel.ID]; math.Abs(got-tc.want) > 1e-8 {
				t.Fatalf("consumption = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChannelConsumptionTotalsAcceptanceRetainedZeroBaseline(t *testing.T) {
	for _, exactMidnight := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact-midnight=%v", exactMidnight), func(t *testing.T) {
			db := groupSettingsAcceptanceDB(t)
			if err := db.AutoMigrate(&Channel{}, &BalanceSnapshot{}, &BalanceChangeLog{}, &ChannelDailyBalance{}, &OperationLedgerEntry{}); err != nil {
				t.Fatal(err)
			}
			rates := NewRates(db)
			since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.Local)
			channel := Channel{Name: "retained-zero", Type: ChannelTypeNewAPI, BalanceMode: BalanceModeAuto, RechargeMultiplier: 1}
			if err := db.Create(&channel).Error; err != nil {
				t.Fatal(err)
			}
			at := since.Add(-time.Minute)
			if exactMidnight {
				at = since
			}
			if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: 0, SampledAt: at}); err != nil {
				t.Fatal(err)
			}
			if !exactMidnight {
				if _, err := rates.CaptureDailyBalances(since.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			var daily ChannelDailyBalance
			if err := db.Where("channel_id = ? AND day = CAST(? AS date)", channel.ID, since.Format("2006-01-02")).First(&daily).Error; err != nil {
				t.Fatal(err)
			}
			if !daily.Reliable || daily.Balance != 0 {
				t.Fatalf("zero baseline lost provenance: %+v", daily)
			}
			if err := db.Where("channel_id = ?", channel.ID).Delete(&BalanceSnapshot{}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&OperationLedgerEntry{ChannelID: &channel.ID, Direction: "expense", Category: "upstream_recharge", Amount: 30, OccurredAt: since.Add(30 * time.Minute)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := rates.AppendBalance(&BalanceSnapshot{ChannelID: channel.ID, Balance: 20, SampledAt: since.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			totals, err := rates.channelConsumptionTotals(since, since.Add(2*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if got := totals[channel.ID]; math.Abs(got-10) > 1e-8 {
				t.Fatalf("consumption = %v, want 10", got)
			}
		})
	}
}

func TestChannelConsumptionTotalsAcceptanceLegacyDailyMigration(t *testing.T) {
	for _, sqlMigration := range []bool{false, true} {
		t.Run(fmt.Sprintf("sql-migration=%v", sqlMigration), func(t *testing.T) {
			db := groupSettingsAcceptanceDB(t)
			if err := db.Exec(`CREATE TABLE channel_daily_balances (
				id BIGSERIAL PRIMARY KEY, channel_id BIGINT NOT NULL, day DATE NOT NULL,
				balance DOUBLE PRECISION NOT NULL, captured_at TIMESTAMPTZ NOT NULL,
				UNIQUE(channel_id, day))`).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("INSERT INTO channel_daily_balances (channel_id, day, balance, captured_at) VALUES (1, '2026-08-19', 100, '2026-08-19 01:00:00+00')").Error; err != nil {
				t.Fatal(err)
			}
			migrate := func() {
				t.Helper()
				if sqlMigration {
					content, err := os.ReadFile("../../migrations/0015_channel_daily_balance_reliable.sql")
					if err != nil {
						t.Fatal(err)
					}
					if err := db.Exec(string(content)).Error; err != nil {
						t.Fatal(err)
					}
				} else if err := db.AutoMigrate(&ChannelDailyBalance{}); err != nil {
					t.Fatal(err)
				}
			}
			migrate()
			var daily ChannelDailyBalance
			if err := db.First(&daily).Error; err != nil {
				t.Fatal(err)
			}
			if daily.Reliable || daily.Balance != 100 {
				t.Fatalf("legacy baseline must remain untrusted and unchanged: %+v", daily)
			}
			if err := db.Model(&daily).Update("reliable", true).Error; err != nil {
				t.Fatal(err)
			}
			migrate()
			if err := db.First(&daily).Error; err != nil {
				t.Fatal(err)
			}
			if !daily.Reliable {
				t.Fatal("repeated migration reset verified provenance")
			}
		})
	}
}
