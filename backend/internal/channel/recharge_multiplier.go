package channel

import (
	"errors"
	"math"
)

func validateRechargeMultiplier(value *float64, fallback float64) (float64, error) {
	if value == nil {
		return fallback, nil
	}
	if *value <= 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return 0, errors.New("充值倍率必须是大于 0 的有效数字")
	}
	return *value, nil
}
