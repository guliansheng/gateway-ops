package storage

import "math"

// EffectiveRechargeMultiplier keeps legacy, uninitialized channel values at 1.
func (c Channel) EffectiveRechargeMultiplier() float64 {
	if c.RechargeMultiplier <= 0 || math.IsNaN(c.RechargeMultiplier) || math.IsInf(c.RechargeMultiplier, 0) {
		return 1
	}
	return c.RechargeMultiplier
}

// ActualAmount converts upstream credits into the amount paid for them.
func (c Channel) ActualAmount(credits float64) float64 {
	return credits * c.EffectiveRechargeMultiplier()
}

// BalanceAmount converts automatic samples. Manual balances already contain
// actual money from the recharge ledger and settled usage deductions.
func (c Channel) BalanceAmount(balance float64) float64 {
	if c.BalanceMode == BalanceModeManual {
		return balance
	}
	return c.ActualAmount(balance)
}

func (c Channel) CurrentBalanceAmount() *float64 {
	if c.LastBalance != nil {
		balance := c.BalanceAmount(*c.LastBalance)
		return &balance
	}
	if c.BalanceMode == BalanceModeManual {
		balance := c.ManualBalance
		return &balance
	}
	return nil
}
