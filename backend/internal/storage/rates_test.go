package storage

import (
	"testing"
	"time"
)

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
