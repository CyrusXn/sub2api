import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import ChannelStatusV1View from '../ChannelStatusV1View.vue'

const mocks = vi.hoisted(() => ({ list: vi.fn(), status: vi.fn(), route: { query: {} as Record<string, string> } }))
vi.mock('@/api/channelMonitor', () => ({ listPassive: mocks.list, status: mocks.status }))
vi.mock('vue-router', () => ({ useRoute: () => mocks.route, useRouter: () => ({ replace: vi.fn() }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ cachedPublicSettings: { channel_monitor_enabled: true }, showError: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
enableAutoUnmount(afterEach)
afterEach(() => { vi.useRealTimers(); localStorage.clear() })

function detail(availability: number) {
  return { items: [{ id: 1, name: 'Monitor', passive: true, primary_status: 'operational', availability_7d: 99, availability_15d: availability, availability_30d: availability }] }
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  localStorage.clear()
  mocks.route.query = {}
  localStorage.setItem('channel-status-auto-refresh', JSON.stringify({ enabled: true, interval_seconds: 60 }))
  mocks.list.mockResolvedValue(detail(90))
})

function mountView() {
  return shallowMount(ChannelStatusV1View, {
    global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      MonitorHero: { props: ['loading'], emits: ['update:window', 'refresh'], template: `<div :data-loading="loading">
        <button class="fifteen" @click="$emit('update:window', '15d')">15d</button>
        <button class="thirty" @click="$emit('update:window', '30d')">30d</button>
        <button class="refresh" @click="$emit('refresh')">Refresh</button>
      </div>` },
      MonitorCardGrid: { props: ['items', 'window'], template: '<output>{{ items[0]?.[`availability_${window}`] }}</output>' }
    } }
  })
}

describe('被动监控各窗口可用率刷新', () => {
  it.each(['fifteen', 'thirty'])('refreshes the cached %s-day availability on timer ticks', async (tab) => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('.' + tab).trigger('click')
    await flushPromises()
    expect(wrapper.get('output').text()).toBe('90')
    mocks.list.mockResolvedValue(detail(98))
    await vi.advanceTimersByTimeAsync(60000)
    await flushPromises()
    expect(wrapper.get('output').text()).toBe('98')
    expect(mocks.list).toHaveBeenCalledTimes(3)
    expect(mocks.status).not.toHaveBeenCalled()
    expect(wrapper.get('[data-loading]').attributes('data-loading')).toBe('false')
  })

  it('keeps the seven-day view on the list endpoint without extra detail requests', async () => {
    const wrapper = mountView()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(60000)
    await flushPromises()
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(mocks.status).not.toHaveBeenCalled()
    expect(wrapper.get('output').text()).toBe('99')
  })

  it('still refreshes cached availability on manual reload', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('.fifteen').trigger('click')
    await flushPromises()
    mocks.list.mockResolvedValue(detail(98))
    await wrapper.get('.refresh').trigger('click')
    await flushPromises()
    expect(wrapper.get('output').text()).toBe('98')
  })
})
