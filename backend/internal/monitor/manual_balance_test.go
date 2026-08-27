package monitor

import (
	"math"
	"testing"
)

func closeEnough(left, right float64) bool { return math.Abs(left-right) < 1e-9 }

func TestApplyManualUsageInitializesBaselineWithoutHistoricalDeduction(t *testing.T) {
	balance, baseline, deducted, initialized := applyManualUsage(9.98, nil, 1.0557681)
	if !closeEnough(balance, 9.98) || !closeEnough(deducted, 0) || !initialized || baseline == nil || !closeEnough(*baseline, 1.0557681) {
		t.Fatalf("first settlement = balance %.7f baseline %#v deducted %.7f initialized %v", balance, baseline, deducted, initialized)
	}
}

func TestApplyManualUsageDoesNotRepeatIdenticalCumulativeCost(t *testing.T) {
	baseline := 1.0557681
	balance, next, deducted, initialized := applyManualUsage(8.9242319, &baseline, 1.0557681)
	if !closeEnough(balance, 8.9242319) || !closeEnough(deducted, 0) || initialized || next == nil || !closeEnough(*next, baseline) {
		t.Fatalf("repeated settlement = balance %.7f baseline %#v deducted %.7f initialized %v", balance, next, deducted, initialized)
	}
}

func TestApplyManualUsageDeductsOnlyCumulativeIncrease(t *testing.T) {
	baseline := 1.0557681
	balance, next, deducted, initialized := applyManualUsage(8.9242319, &baseline, 1.2557681)
	if !closeEnough(balance, 8.7242319) || !closeEnough(deducted, 0.2) || initialized || next == nil || !closeEnough(*next, 1.2557681) {
		t.Fatalf("increased settlement = balance %.7f baseline %#v deducted %.7f initialized %v", balance, next, deducted, initialized)
	}
}

func TestApplyManualUsageClampsRelayResetToZeroDeduction(t *testing.T) {
	baseline := 1.2557681
	balance, next, deducted, initialized := applyManualUsage(8.7242319, &baseline, 0.1)
	if !closeEnough(balance, 8.7242319) || !closeEnough(deducted, 0) || initialized || next == nil || !closeEnough(*next, 0.1) {
		t.Fatalf("reset settlement = balance %.7f baseline %#v deducted %.7f initialized %v", balance, next, deducted, initialized)
	}
}
