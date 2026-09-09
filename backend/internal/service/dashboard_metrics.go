package service

import (
	"context"
	"time"
)

// DashboardBusinessTotals 是可长期保存并按时间范围汇总的经营指标。
type DashboardBusinessTotals struct {
	RechargeAmount             float64 `json:"recharge_amount"`
	TotalRequests              int64   `json:"total_requests"`
	InputTokens                int64   `json:"input_tokens"`
	OutputTokens               int64   `json:"output_tokens"`
	CacheCreationTokens        int64   `json:"cache_creation_tokens"`
	CacheReadTokens            int64   `json:"cache_read_tokens"`
	TotalTokens                int64   `json:"total_tokens"`
	TotalCost                  float64 `json:"total_cost"`
	ActualCost                 float64 `json:"actual_cost"`
	ActualCostExcludingAdmin   float64 `json:"actual_cost_excluding_admin"`
	AccountCost                float64 `json:"account_cost"`
	AccountCostExcludingAdmin  float64 `json:"account_cost_excluding_admin"`
	UpstreamCost               float64 `json:"upstream_cost"`
	UpstreamCostExcludingAdmin float64 `json:"upstream_cost_excluding_admin"`
}

// DashboardBusinessDailyPoint 支持经营指标按日查询和后续利润分析。
type DashboardBusinessDailyPoint struct {
	BucketDate                 time.Time `json:"bucket_date"`
	RechargeAmount             float64   `json:"recharge_amount"`
	TotalRequests              int64     `json:"total_requests"`
	TotalTokens                int64     `json:"total_tokens"`
	ActualCost                 float64   `json:"actual_cost"`
	ActualCostExcludingAdmin   float64   `json:"actual_cost_excluding_admin"`
	AccountCost                float64   `json:"account_cost"`
	AccountCostExcludingAdmin  float64   `json:"account_cost_excluding_admin"`
	UpstreamCost               float64   `json:"upstream_cost"`
	UpstreamCostExcludingAdmin float64   `json:"upstream_cost_excluding_admin"`
}

type DashboardBusinessSummary struct {
	// Ledger 与请求费用估算分开，缺少真实余额边界时成本、收益均为 nil。
	Ledger DashboardBusinessLedger `json:"ledger"`
	// UpstreamRechargeTotal 直接汇总充值记录，不受经营历史日期范围影响。
	UpstreamRechargeTotal float64 `json:"upstream_recharge_total"`
	// UserBalanceTotal 仅统计有效充值用户当前余额，排除 admin 体验额度。
	UserBalanceTotal float64 `json:"user_balance_total"`
	// UpstreamBalanceTotal 按上游站点取最新最小余额后汇总。
	UpstreamBalanceTotal float64 `json:"upstream_balance_total"`
	// RangeUpstreamRechargeTotal 按充值事件发生时间过滤后的区间上游充值。
	RangeUpstreamRechargeTotal float64 `json:"range_upstream_recharge_total"`
	// RangeUserBalanceTotal 取所选结束日的用户总余额快照；nil 表示结束日尚未采集到快照。
	RangeUserBalanceTotal *float64 `json:"range_user_balance_total"`
	// RangeUpstreamBalanceTotal 兼容旧客户端的结束日旧版上游余额快照（实账使用 Ledger）；nil 表示结束日尚未采集到快照。
	RangeUpstreamBalanceTotal *float64 `json:"range_upstream_balance_total"`
	// RangeBalanceSnapshotDate 上述两个余额快照对应的自然日，便于前端说明"截至某日"。
	RangeBalanceSnapshotDate *time.Time                    `json:"range_balance_snapshot_date"`
	Lifetime                 DashboardBusinessTotals       `json:"lifetime"`
	Range                    DashboardBusinessTotals       `json:"range"`
	Daily                    []DashboardBusinessDailyPoint `json:"daily"`
}

// DashboardBusinessLedger 按人民币 1:1 记录现金与订阅剩余资产，不重复计入订阅购买价。
type DashboardBusinessLedger struct {
	CashBalance          *float64 `json:"cash_balance"`
	SubscriptionBalance  *float64 `json:"subscription_balance"`
	TotalBalance         *float64 `json:"total_balance"`
	KnownBalanceSubtotal float64  `json:"known_balance_subtotal"`
	UnknownSites         int64    `json:"unknown_sites"`
	// 累计仅是录入起点余额为零的账本估值，不声称核验过起点资产。
	LifetimeConsumptionEstimate *float64   `json:"lifetime_consumption_estimate"`
	LifetimeProfitEstimate      *float64   `json:"lifetime_profit_estimate"`
	OpeningBalance              *float64   `json:"opening_balance"`
	ClosingBalance              *float64   `json:"closing_balance"`
	OpeningCapturedAt           *time.Time `json:"opening_captured_at"`
	ClosingCapturedAt           *time.Time `json:"closing_captured_at"`
	RangeConsumption            *float64   `json:"range_consumption"`
	RangeProfit                 *float64   `json:"range_profit"`
	RangeStatus                 string     `json:"range_status"`
}

type DashboardLowBalanceAccount struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Platform   string     `json:"platform"`
	Balance    float64    `json:"balance"`
	Unit       string     `json:"unit"`
	ReceivedAt *time.Time `json:"received_at"`
}

type DashboardSystemMetricPoint struct {
	Time                          time.Time `json:"time"`
	CPUUsagePercent               *float64  `json:"cpu_usage_percent"`
	MemoryUsedMB                  *int64    `json:"memory_used_mb"`
	MemoryTotalMB                 *int64    `json:"memory_total_mb"`
	MemoryUsagePercent            *float64  `json:"memory_usage_percent"`
	NetworkReceiveBytesPerSecond  *float64  `json:"network_receive_bytes_per_second"`
	NetworkTransmitBytesPerSecond *float64  `json:"network_transmit_bytes_per_second"`
	DiskUsedBytes                 *int64    `json:"disk_used_bytes"`
	DiskTotalBytes                *int64    `json:"disk_total_bytes"`
	DiskUsagePercent              *float64  `json:"disk_usage_percent"`
	ResourceSource                string    `json:"resource_source"`
}

type DashboardNetworkTrafficDailyPoint struct {
	BucketDate    string `json:"bucket_date"`
	ReceiveBytes  int64  `json:"receive_bytes"`
	TransmitBytes int64  `json:"transmit_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
}

type DashboardNetworkTrafficTotals struct {
	ReceiveBytes  int64 `json:"receive_bytes"`
	TransmitBytes int64 `json:"transmit_bytes"`
	TotalBytes    int64 `json:"total_bytes"`
}

type DashboardSystemMetricTrend struct {
	Source        string                              `json:"source"`
	Points        []DashboardSystemMetricPoint        `json:"points"`
	NetworkDaily  []DashboardNetworkTrafficDailyPoint `json:"network_daily"`
	NetworkTotals DashboardNetworkTrafficTotals       `json:"network_totals"`
}

type DashboardMetricsRepository interface {
	GetDashboardBusinessSummary(ctx context.Context, start, end time.Time) (*DashboardBusinessSummary, error)
	ListDashboardLowBalanceAccounts(ctx context.Context, threshold float64, limit int) ([]DashboardLowBalanceAccount, error)
	GetDashboardSystemMetricTrend(ctx context.Context, start, end time.Time, maxPoints int) (*DashboardSystemMetricTrend, error)
	GetDashboardLast24HourUsage(ctx context.Context, start, end time.Time) (int64, float64, error)
}

// DashboardConcurrencyReader 只暴露仪表盘需要的总并发读取能力，避免扩大网关热路径接口。
type DashboardConcurrencyReader interface {
	GetTotalActiveConcurrency(ctx context.Context) (int, error)
}

type DashboardRealtimeMetrics struct {
	ActiveRequests      int     `json:"active_requests"`
	RequestsPerMinute   int64   `json:"requests_per_minute"`
	TokensPerMinute     int64   `json:"tokens_per_minute"`
	AverageResponseTime float64 `json:"average_response_time"`
	ErrorRate           float64 `json:"error_rate"`
}
