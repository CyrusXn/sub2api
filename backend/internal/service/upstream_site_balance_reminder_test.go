package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvaluateUpstreamBalanceIncrease(t *testing.T) {
	tests := []struct {
		name     string
		previous *float64
		current  float64
		want     float64
	}{
		{name: "first observation is baseline", current: 20},
		{name: "increase creates reminder", previous: float64Ptr(10), current: 15, want: 5},
		{name: "unchanged balance does not create reminder", previous: float64Ptr(10), current: 10},
		{name: "decrease does not create reminder", previous: float64Ptr(10), current: 8},
		{name: "floating point noise does not create reminder", previous: float64Ptr(10), current: 10.0000000001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			increase, delta := EvaluateUpstreamBalanceIncrease(tt.previous, tt.current)
			require.Equal(t, tt.want > 0, increase)
			if increase {
				require.InDelta(t, tt.want, delta, 1e-9)
			} else {
				require.Zero(t, delta)
			}
		})
	}
}

func TestIsUpstreamSiteLoginExpired(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{name: "unauthorized status", statusCode: http.StatusUnauthorized, body: `{}`, want: true},
		{name: "explicit unauthenticated response", statusCode: http.StatusForbidden, body: `{"message":"未登录"}`, want: true},
		{name: "ordinary forbidden response", statusCode: http.StatusForbidden, body: `{"message":"无权限"}`},
		{name: "ordinary server response", statusCode: http.StatusInternalServerError, body: `{"message":"upstream error"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isUpstreamSiteLoginExpired(tt.statusCode, []byte(tt.body)))
		})
	}
}
