package service

import (
	"errors"
	"math"
)

func validateAdminUsageMultiplier(value *float64) error {
	if value == nil {
		return nil
	}
	if *value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return errors.New("admin_usage_multiplier must be >= 0")
	}
	return nil
}
