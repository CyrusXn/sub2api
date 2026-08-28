import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import type { DashboardStats } from '@/types'
import { useAdminRealtimeMetricsStore } from '@/stores/adminRealtimeMetrics'
import DashboardView from '../DashboardView.vue'

const {
  getSnapshotV2,
  getUserUsageTrend,
  getUserSpendingRanking,
  getBusinessSummary,
  getSystemMetricsTrend,
  getRealtimeMetrics
} = vi.hoisted(() => ({
  getSnapshotV2: vi.fn(),
  getUserUsageTrend: vi.fn(),
  getUserSpendingRanking: vi.fn(),
  getBusinessSummary: vi.fn(),
  getSystemMetricsTrend: vi.fn(),
  getRealtimeMetrics: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getSnapshotV2,
      getUserUsageTrend,
      getUserSpendingRanking,
      getBusinessSummary,
      getSystemMetricsTrend,
      getRealtimeMetrics
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

const createDashboardStats = (): DashboardStats => ({
  total_users: 0,
  today_new_users: 0,
  active_users: 0,
  hourly_active_users: 0,
  stats_updated_at: '',
  stats_stale: false,
  total_api_keys: 0,
  active_api_keys: 0,
  total_accounts: 0,
  normal_accounts: 0,
  error_accounts: 0,
  ratelimit_accounts: 0,
  overload_accounts: 0,
  total_requests: 0,
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_creation_tokens: 0,
  total_cache_read_tokens: 0,
  total_tokens: 0,
  total_cost: 0,
  total_actual_cost: 0,
  today_requests: 0,
  today_input_tokens: 0,
  today_output_tokens: 0,
  today_cache_creation_tokens: 0,
  today_cache_read_tokens: 0,
  today_tokens: 0,
  today_cost: 0,
  today_actual_cost: 0,
  average_duration_ms: 0,
  uptime: 0,
  rpm: 0,
  tpm: 0
})

describe('admin DashboardView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())

    getSnapshotV2.mockReset()
    getUserUsageTrend.mockReset()
    getUserSpendingRanking.mockReset()
    getBusinessSummary.mockReset()
    getSystemMetricsTrend.mockReset()
    getRealtimeMetrics.mockReset()

    getSnapshotV2.mockResolvedValue({
      stats: createDashboardStats(),
      trend: [],
      models: []
    })
    getUserUsageTrend.mockResolvedValue({
      trend: [],
      start_date: '',
      end_date: '',
      granularity: 'hour'
    })
    getUserSpendingRanking.mockResolvedValue({
      ranking: [],
      total_actual_cost: 0,
      total_requests: 0,
      total_tokens: 0,
      start_date: '',
      end_date: ''
    })
    getBusinessSummary.mockResolvedValue({
      upstream_recharge_total: 0,
      user_balance_total: 0,
      upstream_balance_total: 0,
      lifetime: {},
      range: {},
      daily: []
    })
    getSystemMetricsTrend.mockResolvedValue({ points: [], source: 'host' })
    getRealtimeMetrics.mockResolvedValue({
      active_requests: 3,
      requests_per_minute: 12,
      tokens_per_minute: 1200,
      average_response_time: 500,
      error_rate: 0
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('uses last 24 hours as default dashboard range', async () => {
    mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour'
    }))
  })

  it('uses dashboard RPM and TPM as compatibility values for older realtime responses', async () => {
    const dashboardStats = createDashboardStats()
    dashboardStats.rpm = 18
    dashboardStats.tpm = 4567
    getSnapshotV2.mockResolvedValue({ stats: dashboardStats, trend: [], models: [] })

    mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          SystemMetricTrendCard: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(useAdminRealtimeMetricsStore().metrics?.requests_per_minute).toBe(18)
    expect(useAdminRealtimeMetricsStore().metrics?.tokens_per_minute).toBe(4567)
  })

  it('does not request retired host metric trends from the business dashboard', async () => {
    mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          SystemMetricTrendCard: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(getBusinessSummary).toHaveBeenCalledTimes(1)
    expect(getSystemMetricsTrend).not.toHaveBeenCalled()
  })

  it('shows the business summary error instead of reporting it as zero', async () => {
    getBusinessSummary.mockRejectedValueOnce(new Error('business failed'))

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          SystemMetricTrendCard: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.dashboard.businessSummaryLoadFailed')
  })

  it('shows all permanent business metrics for the selected range', async () => {
    getBusinessSummary.mockResolvedValueOnce({
      upstream_recharge_total: 8932.97,
      user_balance_total: 321.45,
      upstream_balance_total: 678.9,
      lifetime: {
        recharge_amount: 100,
        actual_cost: 80,
        actual_cost_excluding_admin: 70
      },
      range: {
        recharge_amount: 11.11,
        total_tokens: 22222,
        actual_cost: 33.33,
        actual_cost_excluding_admin: 22.22,
        account_cost: 9.99
      },
      daily: []
    })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          SystemMetricTrendCard: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const text = wrapper.text()
    expect(text).toContain('admin.dashboard.selectedRange')
    expect(text).toContain('admin.dashboard.rangeRecharge')
    expect(text).toContain('admin.dashboard.rangeTokens')
    expect(text).toContain('admin.dashboard.rangeConsumption')
    expect(text).toContain('admin.dashboard.userTotalRecharge')
    expect(text).toContain('admin.dashboard.rangeAccountCost')
    expect(text).toContain('$11.11')
    expect(text).toContain('22.22K')
    expect(text).toContain('$33.33')
    expect(text).toContain('$22.22')
    expect(wrapper.get('[data-test="dashboard-user-total-recharge"]').text()).toBe('$100.00')
    expect(text).toContain('$9.99')
    expect(wrapper.get('[data-test="upstream-recharge-total"]').text()).toBe('$8,932.97')
  })

  it('shows today and last 24 hour consumption and refreshes the whole top metric block', async () => {
    const dashboardStats = createDashboardStats()
    dashboardStats.today_tokens = 1234
    dashboardStats.last_24_hour_tokens = 5678
    dashboardStats.today_actual_cost = 12.34
    ;(dashboardStats as DashboardStats & { last_24_hour_actual_cost: number }).last_24_hour_actual_cost = 56.78
    getSnapshotV2.mockResolvedValue({ stats: dashboardStats, trend: [], models: [] })

    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: {
            emits: ['refresh-admin-metrics'],
            template: '<div><button data-testid="dashboard-top-metrics-refresh" @click="$emit(\'refresh-admin-metrics\')" /><slot /></div>'
          },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          SystemMetricTrendCard: true,
          Line: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.text()).toContain('admin.dashboard.todayTotalConsumption')
    expect(wrapper.text()).toContain('admin.dashboard.last24HourTotalConsumption')
    expect(wrapper.text()).toContain('$12.34')
    expect(wrapper.text()).toContain('$56.78')

    const refreshButton = wrapper.get('[data-testid="dashboard-top-metrics-refresh"]')
    await refreshButton.trigger('click')
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledTimes(2)
    expect(getSnapshotV2).toHaveBeenLastCalledWith(expect.objectContaining({
      include_stats: true,
      include_trend: false,
      include_model_stats: false,
      include_group_stats: false,
      include_users_trend: false
    }))
  })

  it('shows a placeholder when the online backend has not returned 24-hour tokens yet', async () => {
    const wrapper = mount(DashboardView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot name="header-status" /><slot /></div>' },
          LoadingSpinner: true,
          Icon: true,
          DateRangePicker: true,
          Select: true,
          ModelDistributionChart: true,
          TokenUsageTrend: true,
          SystemMetricTrendCard: true,
          Line: true
        }
      }
    })

    await flushPromises()

    const label = wrapper.findAll('p').find(node => node.text() === 'admin.dashboard.last24HourTokens')
    expect(label?.element.nextElementSibling?.textContent).toBe('--')
  })

})
