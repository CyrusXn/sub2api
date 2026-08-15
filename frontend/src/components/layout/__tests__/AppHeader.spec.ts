import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AppHeader from '@/components/layout/AppHeader.vue'

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
})
