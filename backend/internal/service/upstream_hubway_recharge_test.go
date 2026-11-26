package service

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHubwayRechargeHistoryFiltersBySourceAndConverts(t *testing.T) {
	since := time.Now().UTC()
	at := since.Add(time.Minute)
	items := []hubwayHistoryItem{
		{5, "daily_benefit", "used", 5, at},
		{4, "admin_balance", "used", 20, at},
		{3, "balance", "used", 200, at},
		{2, "balance", "unused", 500, at},
		{1, "balance", "used", 100, since.Add(-time.Minute)},
	}
	total := len(items)
	recharges, latest, err := collectHubwayRecharges(since, .1, func(page int) (hubwayHistoryPage, error) {
		require.Equal(t, 1, page)
		return hubwayHistoryPage{Total: &total, Items: items}, nil
	})
	require.NoError(t, err)
	require.Equal(t, at, latest)
	require.Equal(t, []UpstreamSiteRecharge{{ID: 3, Amount: 20, UsedAt: at}}, recharges)
}

func TestHubwayRechargeHistoryNeedsCompletePages(t *testing.T) {
	since := time.Now().UTC()
	items := make([]hubwayHistoryItem, hubwayHistoryPageSize)
	for i := range items {
		items[i] = hubwayHistoryItem{int64(102 - i), "balance", "used", 200, since}
	}
	total := len(items) + 1
	for _, fail := range []bool{false, true} {
		rows, latest, err := collectHubwayRecharges(since, .1, func(page int) (hubwayHistoryPage, error) {
			if page == 1 {
				return hubwayHistoryPage{Total: &total, Items: items}, nil
			}
			if fail {
				return hubwayHistoryPage{}, errors.New("拉取失败")
			}
			return hubwayHistoryPage{Total: &total, Items: []hubwayHistoryItem{{2, "balance", "used", 10, since}}}, nil
		})
		if fail {
			require.Error(t, err)
			require.Nil(t, rows)
			require.True(t, latest.IsZero())
		} else {
			require.NoError(t, err)
			require.Len(t, rows, total)
			require.Equal(t, 1.0, rows[len(rows)-1].Amount)
		}
	}
}
