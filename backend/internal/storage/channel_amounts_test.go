package storage

import (
	"math"
	"testing"
)

func TestChannelBalanceAmounts(t *testing.T) {
	raw := 100.0
	for _, test := range []struct {
		name    string
		channel Channel
		want    float64
	}{
		{"legacy default", Channel{LastBalance: &raw}, 100},
		{"automatic discount", Channel{LastBalance: &raw, RechargeMultiplier: 0.1}, 10},
		{"automatic premium", Channel{LastBalance: &raw, RechargeMultiplier: 2}, 200},
		{"manual money", Channel{BalanceMode: BalanceModeManual, LastBalance: &raw, RechargeMultiplier: 0.1}, 100},
		{"manual fallback", Channel{BalanceMode: BalanceModeManual, ManualBalance: 12, RechargeMultiplier: 0.1}, 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := test.channel.CurrentBalanceAmount()
			if got == nil || math.Abs(*got-test.want) > 1e-9 {
				t.Fatalf("balance = %v, want %v", got, test.want)
			}
			if raw != 100 {
				t.Fatal("conversion changed stored balance")
			}
		})
	}
	if got := (Channel{}).CurrentBalanceAmount(); got != nil {
		t.Fatalf("unknown balance = %v, want nil", got)
	}
}
