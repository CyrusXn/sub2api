package service

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBalanceCenterReviewPositionPersistsPerAdmin(t *testing.T) {
	ctx := context.Background()
	repo := &balanceCenterSettingRepoStub{values: make(map[string]string)}
	svc := NewBalanceCenterService(nil, repo)
	position := RechargeReviewPosition{X: 0, Y: 0.75}
	require.NoError(t, svc.SaveRechargeReviewPosition(ctx, 5, position))

	// 重新实例化服务模拟刷新后从服务端重新读取，而非复用内存状态。
	svc = NewBalanceCenterService(nil, repo)
	got, err := svc.GetRechargeReviewPosition(ctx, 5)
	require.NoError(t, err)
	require.Equal(t, &position, got)
	defaultPosition, err := svc.GetRechargeReviewPosition(ctx, 6)
	require.NoError(t, err)
	require.Nil(t, defaultPosition)

	for _, invalid := range []RechargeReviewPosition{{X: -1}, {Y: 2}, {X: math.NaN()}, {Y: math.Inf(1)}} {
		require.Error(t, svc.SaveRechargeReviewPosition(ctx, 5, invalid))
	}
	got, err = svc.GetRechargeReviewPosition(ctx, 5)
	require.NoError(t, err)
	require.Equal(t, &position, got)
}
