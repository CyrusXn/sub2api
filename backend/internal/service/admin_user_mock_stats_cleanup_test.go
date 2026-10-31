package service

import (
	"context"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestUnimplementedUserUsageStatsDoesNotReturnSyntheticZeros(t *testing.T) {
	stats, err := (&adminServiceImpl{}).GetUserUsageStats(context.Background(), 1, "day")
	require.Nil(t, stats)
	require.Equal(t, http.StatusNotImplemented, infraerrors.Code(err))
}
