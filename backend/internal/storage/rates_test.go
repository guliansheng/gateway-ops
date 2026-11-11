package storage

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestListRecentGroupsSinceReturnsEmptyArray(t *testing.T) {
	since := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	for _, limit := range []int{50, 0, -1} {
		t.Run(fmt.Sprintf("limit=%d", limit), func(t *testing.T) {
			repo, _ := newDegradationSQL(t, degradationSQLStep{
				contains: `FROM "rate_snapshots"`, columns: []string{"id"},
				check: func(_ string, args []driver.NamedValue) {
					if len(args) != 2 || args[0].Value != since || args[1].Value != int64(50) {
						t.Fatalf("query arguments = %v, want since and limit 50", args)
					}
				},
			})
			groups, err := NewRates(repo.db).ListRecentGroupsSince(since, limit)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(groups)
			if err != nil || string(data) != "[]" {
				t.Fatalf("empty groups JSON = %s, err = %v; want []", data, err)
			}
		})
	}
}

func TestListRecentGroupsSincePropagatesQueryError(t *testing.T) {
	queryErr := errors.New("recent groups query failed")
	repo, _ := newDegradationSQL(t, degradationSQLStep{contains: `FROM "rate_snapshots"`, err: queryErr})
	if _, err := NewRates(repo.db).ListRecentGroupsSince(time.Time{}, 50); !errors.Is(err, queryErr) {
		t.Fatalf("query error = %v, want %v", err, queryErr)
	}
}

func TestIsTransientBalanceReset(t *testing.T) {
	at := time.Date(2026, 8, 19, 13, 5, 0, 0, time.UTC)
	reset := BalanceChangeLog{
		ChannelID:       20,
		PreviousBalance: 99.90635868,
		NewBalance:      0,
		Delta:           -99.90635868,
		DetectedAt:      at,
	}
	recovery := BalanceChangeLog{
		ChannelID:       20,
		PreviousBalance: 0,
		NewBalance:      99.99993544,
		Delta:           99.99993544,
		DetectedAt:      at.Add(5 * time.Minute),
	}
	if !isTransientBalanceReset(reset, recovery) {
		t.Fatal("expected a zero-to-near-original recovery to be treated as transient")
	}

	tooLate := recovery
	tooLate.DetectedAt = at.Add(transientResetWindow + time.Second)
	if isTransientBalanceReset(reset, tooLate) {
		t.Fatal("did not expect a delayed recovery to be treated as transient")
	}

	partial := recovery
	partial.NewBalance = 50
	partial.Delta = 50
	if isTransientBalanceReset(reset, partial) {
		t.Fatal("did not expect a partial recovery to be treated as transient")
	}
}

func TestCapConsumptionToNet(t *testing.T) {
	start, end := 100.0, 99.8539142
	got := capConsumptionToNet(100.1460858, &start, &end)
	want := start - end
	if got != want {
		t.Fatalf("capConsumptionToNet() = %.8f, want %.8f", got, want)
	}

	if got := capConsumptionToNet(0.1, &start, &start); got != 0.1 {
		t.Fatalf("expected a non-decreasing range to preserve the observed consumption, got %.8f", got)
	}
	if got := capConsumptionToNet(-1, &start, &end); got != 0 {
		t.Fatalf("expected negative consumption to be clamped to zero, got %.8f", got)
	}
}

func TestRechargeBalanceUnitsRespectChannelMultiplier(t *testing.T) {
	got := rechargeBalanceUnits(balanceRangeBound{RechargeMultiplier: 0.1}, 10)
	if got != 100 {
		t.Fatalf("automatic recharge units = %v, want 100", got)
	}
	got = rechargeBalanceUnits(balanceRangeBound{BalanceMode: BalanceModeManual, RechargeMultiplier: 0.1}, 10)
	if got != 10 {
		t.Fatalf("manual recharge units = %v, want 10", got)
	}
}
