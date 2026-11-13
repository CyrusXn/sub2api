import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AppHeader from '@/components/layout/AppHeader.vue'
import Select from '@/components/common/Select.vue'

enableAutoUnmount(afterEach)

const { getRealtimeMetrics, authStore } = vi.hoisted(() => ({
  getRealtimeMetrics: vi.fn(),
  authStore: {
    isAdmin: true,
    isSimpleMode: false,
    user: {
      role: 'admin',
      username: 'admin',
      email: 'admin@example.com',
      balance: 0,
      frozen_balance: 0,
      avatar_url: ''
    },
    logout: vi.fn()
  }
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    dashboard: {
      getRealtimeMetrics
    }
  }
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    contactInfo: '',
    docUrl: '',
    cachedPublicSettings: null,
    toggleMobileSidebar: vi.fn()
  }),
  useAuthStore: () => authStore,
  useOnboardingStore: () => ({ replay: vi.fn() })
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] })
}))

vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key })
}))

const createTestRouter = () => createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/admin/dashboard', component: { template: '<div />' } },
    { path: '/admin/users', component: { template: '<div />' } },
    { path: '/admin/accounts', component: { template: '<div />' } }
  ]
})

const mountHeader = async (path = '/admin/users') => {
  const router = createTestRouter()
  await router.push(path)
  await router.isReady()

  const wrapper = mount(AppHeader, {
    global: {
      plugins: [createPinia(), router],
      stubs: {
        AnnouncementBell: true,
        LocaleSwitcher: true,
        SubscriptionProgressMini: true,
        Icon: { template: '<span />' }
      }
    }
  })

  await flushPromises()
  return { router, wrapper }
}

describe('AppHeader 管理员实时性能', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    authStore.isAdmin = true
    authStore.user.role = 'admin'
    getRealtimeMetrics.mockReset()
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
    localStorage.clear()
  })

  it('管理员在任意页面可见，切换页面时刷新且不会定时轮询', async () => {
    const { router, wrapper } = await mountHeader()

    expect(wrapper.text()).toContain('admin.dashboard.realtimePerformance')
    expect(wrapper.text()).toContain('RPM')
    expect(wrapper.text()).toContain('TPM')
    expect(wrapper.text()).toContain('admin.dashboard.currentConcurrency')
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(1)

    await router.push('/admin/accounts')
    await flushPromises()
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(30_000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
  })

  it('普通用户不显示，也不请求管理员实时性能接口', async () => {
    authStore.isAdmin = false
    authStore.user.role = 'user'

    const { wrapper } = await mountHeader()

    expect(wrapper.text()).not.toContain('admin.dashboard.realtimePerformance')
    expect(getRealtimeMetrics).not.toHaveBeenCalled()
  })

  it('仪表盘顶部刷新会通知页面刷新整块统计', async () => {
    const { wrapper } = await mountHeader('/admin/dashboard')

    await wrapper.get('[data-testid="admin-realtime-refresh"]').trigger('click')
    await flushPromises()

    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
    expect(wrapper.emitted('refresh-admin-metrics')).toHaveLength(1)
  })

  it('顶部自动刷新独立设置周期，关闭后停止请求', async () => {
    const accountSettings = JSON.stringify({ enabled: true, interval_seconds: 30 })
    localStorage.setItem('account-auto-refresh', accountSettings)
    const { wrapper } = await mountHeader()
    const select = wrapper.getComponent(Select)
    expect(select.props('searchable')).toBe(true)
    expect(select.props('options').map(option => option.value)).toEqual([0, 5, 10, 15, 30])

    select.vm.$emit('update:modelValue', 5)
    await vi.advanceTimersByTimeAsync(5000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
    expect(localStorage.getItem('account-auto-refresh')).toBe(accountSettings)

    select.vm.$emit('update:modelValue', 15)
    await vi.advanceTimersByTimeAsync(14_000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(3)

    select.vm.$emit('update:modelValue', 0)
    await vi.advanceTimersByTimeAsync(30_000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(3)
    expect(JSON.parse(localStorage.getItem('admin-realtime-auto-refresh')!)).toEqual({
      enabled: false, interval_seconds: 15
    })
  })

  it('恢复顶部刷新配置并更新当前并发，卸载后不再请求', async () => {
    localStorage.setItem('admin-realtime-auto-refresh', JSON.stringify({ enabled: true, interval_seconds: 5 }))
    const { wrapper } = await mountHeader('/admin/dashboard')
    getRealtimeMetrics.mockResolvedValue({ active_requests: 19, requests_per_minute: 42, tokens_per_minute: 5678 })

    await vi.advanceTimersByTimeAsync(5000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('19')
    expect(wrapper.text()).toContain('42')
    expect(wrapper.emitted('refresh-admin-metrics')).toBeUndefined()

    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
  })

  it('顶部自动刷新不会与未完成的手动请求重复执行', async () => {
    const { wrapper } = await mountHeader()
    wrapper.getComponent(Select).vm.$emit('update:modelValue', 5)
    let resolve!: (value: unknown) => void
    getRealtimeMetrics.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    await wrapper.get('[data-testid="admin-realtime-refresh"]').trigger('click')

    await vi.advanceTimersByTimeAsync(15_000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(2)
    resolve({ active_requests: 7 })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    expect(getRealtimeMetrics).toHaveBeenCalledTimes(3)
  })
})
