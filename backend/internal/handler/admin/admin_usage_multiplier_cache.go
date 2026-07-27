package admin

// clearAdminUsageMultiplierCaches 让倍率修改后的管理端统计立即按新口径回源。
func clearAdminUsageMultiplierCaches() {
	usageStatsCache.Clear()
	dashboardTrendCache.Clear()
	dashboardModelStatsCache.Clear()
	dashboardGroupStatsCache.Clear()
	dashboardUsersTrendCache.Clear()
	dashboardAPIKeysTrendCache.Clear()
	dashboardSnapshotV2Cache.Clear()
	dashboardUsersRankingCache.Clear()
	dashboardBatchUsersUsageCache.Clear()
	dashboardBatchAPIKeysUsageCache.Clear()
}
