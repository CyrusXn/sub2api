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
	Lifetime DashboardBusinessTotals       `json:"lifetime"`
	Range    DashboardBusinessTotals       `json:"range"`
	Daily    []DashboardBusinessDailyPoint `json:"daily"`
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
