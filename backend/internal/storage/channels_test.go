package storage

import "testing"

func TestCarryForwardManualBalanceChangePreservesConcurrentRecharge(t *testing.T) {
	currentBalance := 52.0
	got := carryForwardManualBalanceChange(42, 41.5, &currentBalance)
	if got != 51.5 {
		t.Fatalf("settled balance = %v, want 51.5", got)
	}
}
