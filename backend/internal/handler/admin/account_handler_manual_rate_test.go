package admin

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateAccountRequestUpstreamBillingManualRateMultiplierPresence(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantSet   bool
		wantValue *float64
		wantError bool
	}{
		{name: "字段缺失", payload: `{}`},
		{name: "设置零倍率", payload: `{"upstream_billing_manual_rate_multiplier":0}`, wantSet: true, wantValue: float64Pointer(0)},
		{name: "设置正倍率", payload: `{"upstream_billing_manual_rate_multiplier":0.03}`, wantSet: true, wantValue: float64Pointer(0.03)},
		{name: "显式清除", payload: `{"upstream_billing_manual_rate_multiplier":null}`, wantSet: true},
		{name: "拒绝字符串", payload: `{"upstream_billing_manual_rate_multiplier":"0.03"}`, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request UpdateAccountRequest
			err := json.Unmarshal([]byte(tt.payload), &request)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantSet, request.UpstreamBillingManualRateMultiplier.Set)
			if tt.wantValue == nil {
				require.Nil(t, request.UpstreamBillingManualRateMultiplier.Value)
				return
			}
			require.NotNil(t, request.UpstreamBillingManualRateMultiplier.Value)
			require.Equal(t, *tt.wantValue, *request.UpstreamBillingManualRateMultiplier.Value)
		})
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}
