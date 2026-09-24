package channel

import (
	"math"
	"testing"
)

func TestRechargeMultiplierValidation(t *testing.T) {
	if got, err := validateRechargeMultiplier(nil, 1); err != nil || got != 1 {
		t.Fatalf("omitted multiplier = %v, %v", got, err)
	}
	for _, value := range []float64{0.1, 1, 10} {
		if got, err := validateRechargeMultiplier(&value, 1); err != nil || got != value {
			t.Fatalf("valid multiplier %v = %v, %v", value, got, err)
		}
	}
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		service := &Service{}
		if _, err := service.Create(CreateInput{RechargeMultiplier: &value}); err == nil {
			t.Fatalf("create accepted invalid multiplier %v", value)
		}
		if _, err := service.Update(1, UpdateInput{RechargeMultiplier: &value}); err == nil {
			t.Fatalf("update accepted invalid multiplier %v", value)
		}
	}
}
