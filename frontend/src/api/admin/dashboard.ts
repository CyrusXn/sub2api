/**
 * Admin Dashboard API endpoints
 * Provides system-wide statistics and metrics
 */

import { apiClient } from '../client'
import type {
  DashboardStats,
  TrendDataPoint,
  ModelStat,
  GroupStat,
  ApiKeyUsageTrendPoint,
  UserUsageTrendPoint,
  UserSpendingRankingResponse,
  UserBreakdownItem,
  UsageRequestType
} from '@/types'

/**
 * Get dashboard statistics
 * @returns Dashboard statistics including users, keys, accounts, and token usage
 */
export async function getStats(): Promise<DashboardStats> {
  const { data } = await apiClient.get<DashboardStats>('/admin/dashboard/stats')
  return data
}

/**
 * Get real-time metrics
 * @returns Real-time system metrics
 */
export interface DashboardRealtimeMetrics {
  active_requests: number
  requests_per_minute: number
  tokens_per_minute: number
  average_response_time: number
  error_rate: number
}

export async function getRealtimeMetrics(): Promise<DashboardRealtimeMetrics> {
  const { data } = await apiClient.get<DashboardRealtimeMetrics>('/admin/dashboard/realtime')
  return data
}

export interface DashboardBusinessTotals {
  recharge_amount: number
  total_requests: number
  input_tokens: number
  output_tokens: number
  cache_creation_tokens: number
  cache_read_tokens: number
  total_tokens: number
  total_cost: number
  actual_cost: number
  actual_cost_excluding_admin: number
  account_cost: number
  account_cost_excluding_admin: number
  upstream_cost: number
  upstream_cost_excluding_admin: number
}

export interface DashboardBusinessDailyPoint {
  bucket_date: string
  recharge_amount: number
  total_requests: number
  total_tokens: number
  actual_cost: number
  actual_cost_excluding_admin: number
  account_cost: number
  account_cost_excluding_admin: number
  upstream_cost: number
  upstream_cost_excluding_admin: number
}

// 实账余额未知时保持 null，禁止在展示层补成零或裁剪消费。
export interface DashboardBusinessLedger {
  cash_balance: number | null
  subscription_balance: number | null
  total_balance: number | null
  known_balance_subtotal: number
  unknown_sites: number
  lifetime_consumption_estimate: number | null
  lifetime_profit_estimate: number | null
  opening_balance: number | null
  closing_balance: number | null
  opening_captured_at: string | null
  closing_captured_at: string | null
  range_consumption: number | null
  range_profit: number | null
  range_status: 'available' | 'missing_boundary' | 'site_scope_changed' | 'inexact_boundary'
}

export interface DashboardBusinessSummary {
  ledger?: DashboardBusinessLedger
  // 直接来自全部上游充值记录，不随查询日期范围变化。
  upstream_recharge_total: number
  // 用户余额只统计有效充值用户，排除管理员体验额度。
  user_balance_total: number
  // 上游余额按站点取最新最小余额后汇总。
  upstream_balance_total: number
  // 按充值事件时间过滤后的区间上游充值。
  range_upstream_recharge_total: number
  // 所选结束日的旧版余额快照；实账使用 ledger，不复用旧上游余额。
  range_user_balance_total: number | null
  range_upstream_balance_total: number | null
  // 上述余额快照对应的自然日。
  range_balance_snapshot_date: string | null
  lifetime: DashboardBusinessTotals
  range: DashboardBusinessTotals
  daily: DashboardBusinessDailyPoint[]
}

export interface DashboardLowBalanceAccount {
  id: number
  name: string
  platform: string
  balance: number
  unit: string
  received_at: string | null
}

export interface DashboardSystemMetricPoint {
  time: string
  cpu_usage_percent: number | null
  memory_used_mb: number | null
  memory_total_mb: number | null
  memory_usage_percent: number | null
  network_receive_bytes_per_second: number | null
  network_transmit_bytes_per_second: number | null
  disk_used_bytes: number | null
  disk_total_bytes: number | null
  disk_usage_percent: number | null
  resource_source: string
}

export interface DashboardNetworkTrafficDailyPoint {
  bucket_date: string
  receive_bytes: number
  transmit_bytes: number
  total_bytes: number
}

export interface DashboardNetworkTrafficTotals {
  receive_bytes: number
  transmit_bytes: number
  total_bytes: number
}

export interface DashboardSystemMetricTrend {
  source: string
  points: DashboardSystemMetricPoint[]
  network_daily?: DashboardNetworkTrafficDailyPoint[]
  network_totals?: DashboardNetworkTrafficTotals
}

export async function getBusinessSummary(params?: Pick<TrendParams, 'start_date' | 'end_date'>): Promise<DashboardBusinessSummary> {
  const { data } = await apiClient.get<DashboardBusinessSummary>('/admin/dashboard/business-summary', { params })
  return data
}

export async function getLowBalanceAccounts(): Promise<{ accounts: DashboardLowBalanceAccount[]; threshold: number }> {
  const { data } = await apiClient.get<{ accounts: DashboardLowBalanceAccount[]; threshold: number }>('/admin/dashboard/low-balance-accounts')
  return data
}

export async function getSystemMetricsTrend(params?: Pick<TrendParams, 'start_date' | 'end_date'>): Promise<DashboardSystemMetricTrend> {
  const { data } = await apiClient.get<DashboardSystemMetricTrend>('/admin/dashboard/system-metrics-trend', { params })
  return data
}

export interface TrendParams {
  start_date?: string
  end_date?: string
  granularity?: 'day' | 'hour'
  user_id?: number
  api_key_id?: number
  model?: string
  account_id?: number
  group_id?: number
  request_type?: UsageRequestType
  stream?: boolean
  native_compaction_v2?: boolean | null
  billing_type?: number | null
	upstream_model_mismatch?: boolean
  user_ids?: string
  api_key_ids?: string
  account_ids?: string
  group_ids?: string
  models?: string
  request_types?: string
  billing_types?: string
  billing_modes?: string
  upstream_model_mismatches?: string
  upstream_site_account_ids?: string
}

export interface TrendResponse {
  trend: TrendDataPoint[]
  start_date: string
  end_date: string
  granularity: string
}

/**
 * Get usage trend data
 * @param params - Query parameters for filtering
 * @returns Usage trend data
 */
export async function getUsageTrend(params?: TrendParams): Promise<TrendResponse> {
  const { data } = await apiClient.get<TrendResponse>('/admin/dashboard/trend', { params })
  return data
}

export interface ModelStatsParams {
  start_date?: string
  end_date?: string
  user_id?: number
  api_key_id?: number
  model?: string
  model_source?: 'requested' | 'upstream' | 'mapping'
  account_id?: number
  group_id?: number
  request_type?: UsageRequestType
  stream?: boolean
  native_compaction_v2?: boolean | null
  billing_type?: number | null
	upstream_model_mismatch?: boolean
  user_ids?: string
  api_key_ids?: string
  account_ids?: string
  group_ids?: string
  models?: string
  request_types?: string
  billing_types?: string
  billing_modes?: string
  upstream_model_mismatches?: string
  upstream_site_account_ids?: string
}

export interface ModelStatsResponse {
  models: ModelStat[]
  start_date: string
  end_date: string
}

/**
 * Get model usage statistics
 * @param params - Query parameters for filtering
 * @returns Model usage statistics
 */
export async function getModelStats(params?: ModelStatsParams): Promise<ModelStatsResponse> {
  const { data } = await apiClient.get<ModelStatsResponse>('/admin/dashboard/models', { params })
  return data
}

export interface GroupStatsParams {
  start_date?: string
  end_date?: string
  user_id?: number
  api_key_id?: number
  account_id?: number
  group_id?: number
  request_type?: UsageRequestType
  stream?: boolean
  native_compaction_v2?: boolean | null
  billing_type?: number | null
	upstream_model_mismatch?: boolean
}

export interface GroupStatsResponse {
  groups: GroupStat[]
  start_date: string
  end_date: string
}

export interface DashboardSnapshotV2Params extends TrendParams {
  include_stats?: boolean
  include_trend?: boolean
  include_model_stats?: boolean
  include_group_stats?: boolean
  include_users_trend?: boolean
  users_trend_limit?: number
}

export interface DashboardSnapshotV2Stats extends DashboardStats {
  uptime: number
}

export interface DashboardSnapshotV2Response {
  generated_at: string
  start_date: string
  end_date: string
  granularity: string
  stats?: DashboardSnapshotV2Stats
  trend?: TrendDataPoint[]
  models?: ModelStat[]
  groups?: GroupStat[]
  users_trend?: UserUsageTrendPoint[]
}

/**
 * Get group usage statistics
 * @param params - Query parameters for filtering
 * @returns Group usage statistics
 */
export async function getGroupStats(params?: GroupStatsParams): Promise<GroupStatsResponse> {
  const { data } = await apiClient.get<GroupStatsResponse>('/admin/dashboard/groups', { params })
  return data
}

export interface UserBreakdownParams {
  start_date?: string
  end_date?: string
  group_id?: number
  model?: string
  model_source?: 'requested' | 'upstream' | 'mapping'
  endpoint?: string
  endpoint_type?: 'inbound' | 'upstream' | 'path'
  limit?: number
  // Sort column for the ranking (allowlisted server-side; falls back to actual_cost)
  sort_by?: 'total_tokens' | 'input_tokens' | 'output_tokens' | 'cache_tokens' | 'requests' | 'cost' | 'actual_cost'
  // Additional filter conditions
  user_id?: number
  api_key_id?: number
  account_id?: number
  request_type?: UsageRequestType
  stream?: boolean
  native_compaction_v2?: boolean | null
  billing_type?: number | null
  user_ids?: string
  api_key_ids?: string
  account_ids?: string
  group_ids?: string
  models?: string
  request_types?: string
  billing_types?: string
}

export interface UserBreakdownResponse {
  users: UserBreakdownItem[]
  start_date: string
  end_date: string
}

export async function getUserBreakdown(params: UserBreakdownParams): Promise<UserBreakdownResponse> {
  const { data } = await apiClient.get<UserBreakdownResponse>('/admin/dashboard/user-breakdown', {
    params
  })
  return data
}

/**
 * Get dashboard snapshot v2 (aggregated response for heavy admin pages).
 */
export async function getSnapshotV2(params?: DashboardSnapshotV2Params): Promise<DashboardSnapshotV2Response> {
  const { data } = await apiClient.get<DashboardSnapshotV2Response>('/admin/dashboard/snapshot-v2', {
    params
  })
  return data
}

export interface ApiKeyTrendParams extends TrendParams {
  limit?: number
}

export interface ApiKeyTrendResponse {
  trend: ApiKeyUsageTrendPoint[]
  start_date: string
  end_date: string
  granularity: string
}

/**
 * Get API key usage trend data
 * @param params - Query parameters for filtering
 * @returns API key usage trend data
 */
export async function getApiKeyUsageTrend(
  params?: ApiKeyTrendParams
): Promise<ApiKeyTrendResponse> {
  const { data } = await apiClient.get<ApiKeyTrendResponse>('/admin/dashboard/api-keys-trend', {
    params
  })
  return data
}

export interface UserTrendParams extends TrendParams {
  limit?: number
}

export interface UserTrendResponse {
  trend: UserUsageTrendPoint[]
  start_date: string
  end_date: string
  granularity: string
}

export interface UserSpendingRankingParams
  extends Pick<TrendParams, 'start_date' | 'end_date'> {
  limit?: number
}

/**
 * Get user usage trend data
 * @param params - Query parameters for filtering
 * @returns User usage trend data
 */
export async function getUserUsageTrend(params?: UserTrendParams): Promise<UserTrendResponse> {
  const { data } = await apiClient.get<UserTrendResponse>('/admin/dashboard/users-trend', {
    params
  })
  return data
}

/**
 * Get user spending ranking data
 * @param params - Query parameters for filtering
 * @returns User spending ranking data
 */
export async function getUserSpendingRanking(
  params?: UserSpendingRankingParams
): Promise<UserSpendingRankingResponse> {
  const { data } = await apiClient.get<UserSpendingRankingResponse>('/admin/dashboard/users-ranking', {
    params
  })
  return data
}

export interface PlatformUsage {
  platform: string
  today_actual_cost: number
  total_actual_cost: number
}

export interface BatchUserUsageStats {
  user_id: number
  today_actual_cost: number
  total_actual_cost: number
  by_platform?: PlatformUsage[]
}

export interface BatchUsersUsageResponse {
  stats: Record<string, BatchUserUsageStats>
}

/**
 * Get batch usage stats for multiple users
 * @param userIds - Array of user IDs
 * @returns Usage stats map keyed by user ID
 */
export async function getBatchUsersUsage(userIds: number[]): Promise<BatchUsersUsageResponse> {
  const { data } = await apiClient.post<BatchUsersUsageResponse>('/admin/dashboard/users-usage', {
    user_ids: userIds
  })
  return data
}

export interface BatchApiKeyUsageStats {
  api_key_id: number
  today_actual_cost: number
  total_actual_cost: number
}

export interface BatchApiKeysUsageResponse {
  stats: Record<string, BatchApiKeyUsageStats>
}

/**
 * Get batch usage stats for multiple API keys
 * @param apiKeyIds - Array of API key IDs
 * @returns Usage stats map keyed by API key ID
 */
export async function getBatchApiKeysUsage(
  apiKeyIds: number[]
): Promise<BatchApiKeysUsageResponse> {
  const { data } = await apiClient.post<BatchApiKeysUsageResponse>(
    '/admin/dashboard/api-keys-usage',
    {
      api_key_ids: apiKeyIds
    }
  )
  return data
}

export const dashboardAPI = {
  getStats,
  getRealtimeMetrics,
  getBusinessSummary,
  getLowBalanceAccounts,
  getSystemMetricsTrend,
  getUsageTrend,
  getModelStats,
  getGroupStats,
  getSnapshotV2,
  getApiKeyUsageTrend,
  getUserUsageTrend,
  getUserSpendingRanking,
  getBatchUsersUsage,
  getBatchApiKeysUsage
}

export default dashboardAPI
