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

// ValidateAdminUsageMultiplier 暴露给 HTTP 层复用同一套值域校验。
func ValidateAdminUsageMultiplier(value *float64) error {
	return validateAdminUsageMultiplier(value)
}
