import { flushPromises, shallowMount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelStatusV1View from '../ChannelStatusV1View.vue'
import FilterMultiSelect from '@/features/channel-monitor-v2/FilterMultiSelect.vue'
import MonitorHero from '@/components/user/monitor/MonitorHero.vue'
import MonitorCardGrid from '@/components/user/monitor/MonitorCardGrid.vue'
import MonitorDetailDialog from '@/components/user/MonitorDetailDialog.vue'

const { list, route, replace } = vi.hoisted(() => ({ list: vi.fn(), route: { query: {} as Record<string, string> }, replace: vi.fn() }))
vi.mock('@/api/channelMonitor', () => ({ listPassive: list, status: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: { channel_monitor_enabled: true }, showError: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key, te: () => true }),
}))
const mountView = () => shallowMount(ChannelStatusV1View, {
  global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' },
    MonitorHero: { props: ['autoRefresh'], emits: ['refresh', 'update:window'], template: `<div>
      <button class="interval" @click="autoRefresh.setInterval(60)">60 seconds</button>
      <button class="refresh" @click="$emit('refresh')">Refresh</button>
      <button class="disable" @click="autoRefresh.setEnabled(false)">Disable</button>
    </div>` },
  } },
})
let wrapper: ReturnType<typeof mountView>
beforeEach(() => { vi.useFakeTimers(); localStorage.clear(); route.query = {}; replace.mockReset(); list.mockReset().mockResolvedValue({ items: [] }) })
afterEach(() => { wrapper?.unmount(); vi.useRealTimers(); localStorage.clear() })

describe('channel monitor refresh interval', () => {
  it('keeps automatic refresh disabled after reopening the page', async () => {
    wrapper = mountView(); await flushPromises()
    await wrapper.get('.disable').trigger('click')
    wrapper.unmount()
    wrapper = mountView(); await flushPromises()
    const calls = list.mock.calls.length
    await vi.advanceTimersByTimeAsync(240000)
    expect(list).toHaveBeenCalledTimes(calls)
    expect(JSON.parse(localStorage.getItem('channel-status-auto-refresh')!).enabled).toBe(false)
    await wrapper.get('.refresh').trigger('click'); await flushPromises()
    expect(list).toHaveBeenCalledTimes(calls + 1)
  })

  it('starts automatic refresh for a first visit', async () => {
    wrapper = mountView(); await flushPromises()
    await vi.advanceTimersByTimeAsync(120000)
    expect(list.mock.calls.length).toBeGreaterThan(1)
  })

  it('固定每分钟读取一次', async () => {
    wrapper = mountView(); await flushPromises()
    await wrapper.get('.interval').trigger('click')
    await vi.advanceTimersByTimeAsync(59000)
    expect(list).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(list).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(59000)
    expect(list).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(list).toHaveBeenCalledTimes(3)
  })

  it('手动刷新后仍然每分钟读取一次', async () => {
    wrapper = mountView(); await flushPromises()
    await wrapper.get('.interval').trigger('click')
    await wrapper.get('.refresh').trigger('click'); await flushPromises()
    expect(list).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(59000)
    expect(list).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(1000)
    expect(list).toHaveBeenCalledTimes(3)
  })

  it('旧缓存间隔重置为 60 秒，并允许停止页面自动刷新', async () => {
    localStorage.setItem('channel-status-auto-refresh', JSON.stringify({ enabled: true, interval_seconds: 120 }))
    wrapper = mountView(); await flushPromises()
    await vi.advanceTimersByTimeAsync(59000)
    expect(list).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1000)
    expect(list).toHaveBeenCalledTimes(2)
    await wrapper.get('.disable').trigger('click')
    await vi.advanceTimersByTimeAsync(120000)
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('移除平台选择和旧链接筛选，始终读取全部平台并保留窗口切换', async () => {
    route.query = { platform: 'openai', range: '15d' }
    wrapper = mountView(); await flushPromises()
    expect(list).toHaveBeenLastCalledWith({ signal: expect.any(AbortSignal), range: '15d' })
    expect(wrapper.findComponent(FilterMultiSelect).exists()).toBe(false)
    expect(replace).toHaveBeenLastCalledWith({ query: { platform: undefined, range: '15d' } })
    wrapper.findComponent(MonitorHero).vm.$emit('update:window', '30d')
    await flushPromises()
    expect(list).toHaveBeenLastCalledWith({ signal: expect.any(AbortSignal), range: '30d' })
    expect(replace).toHaveBeenLastCalledWith({ query: { platform: undefined, range: '30d' } })
  })

  it('详情使用当前分组记录，汇总状态不会被前面的黄色盖掉红色', async () => {
    const items = [{ id: 1, name: 'slow', passive: true, primary_status: 'degraded' }, { id: 2, passive: true, primary_status: 'error' }]
    list.mockResolvedValue({ items })
    wrapper = mountView(); await flushPromises()
    expect(wrapper.findComponent(MonitorHero).attributes('overall-status')).toBe('unavailable')
    wrapper.findComponent(MonitorCardGrid).vm.$emit('cardClick', items[0])
    await flushPromises()
    expect(wrapper.findComponent(MonitorDetailDialog).props('passiveItem')).toEqual(items[0])
  })
})
