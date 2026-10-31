import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import OpsDashboard from '../OpsDashboard.vue'
import OpsDashboardHeader from '../components/OpsDashboardHeader.vue'
import OpsSystemLogTable from '../components/OpsSystemLogTable.vue'

const api = vi.hoisted(() => ({
  getAdvancedSettings: vi.fn(),
  getDashboardSnapshotV2: vi.fn(),
  getThroughputTrend: vi.fn(),
  getLatencyHistogram: vi.fn(),
  getErrorDistribution: vi.fn(),
  getMetricThresholds: vi.fn(),
}))
vi.mock('@/api/admin/ops', () => ({ opsAPI: api, default: api }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores', () => ({
  useAdminSettingsStore: () => ({ opsMonitoringEnabled: true, fetch: vi.fn().mockResolvedValue(undefined) }),
  useAppStore: () => ({ showError: vi.fn() }),
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.useFakeTimers()
  vi.clearAllMocks()
  api.getAdvancedSettings.mockResolvedValue({ auto_refresh_enabled: true, auto_refresh_interval_seconds: 15 })
  api.getDashboardSnapshotV2.mockResolvedValue({ overview: {}, throughput_trend: {}, error_trend: {} })
  api.getThroughputTrend.mockResolvedValue({})
  api.getLatencyHistogram.mockResolvedValue({})
  api.getErrorDistribution.mockResolvedValue({})
  api.getMetricThresholds.mockResolvedValue({})
})
afterEach(() => vi.useRealTimers())

describe('运维页面到点刷新', () => {
  it('倒计时到零即刷新，使用手动刷新同一路径并同步子列表', async () => {
    const wrapper = shallowMount(OpsDashboard, {
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } },
    })
    await flushPromises()
    expect(api.getDashboardSnapshotV2).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(14999)
    expect(api.getDashboardSnapshotV2).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(api.getDashboardSnapshotV2).toHaveBeenCalledTimes(2)
    expect(wrapper.getComponent(OpsSystemLogTable).props('refreshToken')).toBe(2)
    wrapper.getComponent(OpsDashboardHeader).vm.$emit('refresh')
    await flushPromises()
    expect(api.getDashboardSnapshotV2).toHaveBeenCalledTimes(3)
    expect(wrapper.getComponent(OpsSystemLogTable).props('refreshToken')).toBe(3)
  })
})
