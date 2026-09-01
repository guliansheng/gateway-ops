package monitor

import (
	"math"
	"testing"
)

func closeEnough(left, right float64) bool { return math.Abs(left-right) < 1e-9 }

func TestApplyManualUsageInitializesBaselineWithoutHistoricalDeduction(t *testing.T) {
	balance, baseline, basis, deducted, initialized := applyManualUsage(9.98, nil, "", 1.0557681, "current-basis")
	if !closeEnough(balance, 9.98) || !closeEnough(deducted, 0) || !initialized || baseline == nil || !closeEnough(*baseline, 1.0557681) || basis != "current-basis" {
		t.Fatalf("first settlement = balance %.7f baseline %#v basis %q deducted %.7f initialized %v", balance, baseline, basis, deducted, initialized)
	}
}

func TestApplyManualUsageDoesNotRepeatIdenticalCumulativeCost(t *testing.T) {
	baseline := 1.0557681
	balance, next, basis, deducted, initialized := applyManualUsage(8.9242319, &baseline, "same-basis", 1.0557681, "same-basis")
	if !closeEnough(balance, 8.9242319) || !closeEnough(deducted, 0) || initialized || next == nil || !closeEnough(*next, baseline) || basis != "same-basis" {
		t.Fatalf("repeated settlement = balance %.7f baseline %#v basis %q deducted %.7f initialized %v", balance, next, basis, deducted, initialized)
	}
}

func TestApplyManualUsageDeductsOnlyCumulativeIncrease(t *testing.T) {
	baseline := 1.0557681
	balance, next, basis, deducted, initialized := applyManualUsage(8.9242319, &baseline, "same-basis", 1.2557681, "same-basis")
	if !closeEnough(balance, 8.7242319) || !closeEnough(deducted, 0.2) || initialized || next == nil || !closeEnough(*next, 1.2557681) || basis != "same-basis" {
		t.Fatalf("increased settlement = balance %.7f baseline %#v basis %q deducted %.7f initialized %v", balance, next, basis, deducted, initialized)
	}
}

func TestApplyManualUsageClampsRelayResetToZeroDeduction(t *testing.T) {
	baseline := 1.2557681
	balance, next, basis, deducted, initialized := applyManualUsage(8.7242319, &baseline, "same-basis", 0.1, "same-basis")
	if !closeEnough(balance, 8.7242319) || !closeEnough(deducted, 0) || initialized || next == nil || !closeEnough(*next, 0.1) || basis != "same-basis" {
		t.Fatalf("reset settlement = balance %.7f baseline %#v basis %q deducted %.7f initialized %v", balance, next, basis, deducted, initialized)
	}
}

func TestApplyManualUsageRebasesWithoutDeductionWhenMultiplierChanges(t *testing.T) {
	baseline := 141.0018075852
	balance, next, basis, deducted, initialized := applyManualUsage(42.05661624, &baseline, "multiplier-0.04", 333.868158438, "multiplier-0.10")
	if !closeEnough(balance, 42.05661624) || !closeEnough(deducted, 0) || !initialized || next == nil || !closeEnough(*next, 333.868158438) || basis != "multiplier-0.10" {
		t.Fatalf("changed basis settlement = balance %.8f baseline %#v basis %q deducted %.8f initialized %v", balance, next, basis, deducted, initialized)
	}
}
