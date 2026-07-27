package admin

// clearAdminUsageMultiplierCaches 清理管理统计快照，避免配置更新与新请求并发时短暂读到旧缓存。
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
