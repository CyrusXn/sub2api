import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useAppStore } from '@/stores/app'
import { getPublicSettings } from '@/api/auth'
import type { PublicSettings } from '@/types'

vi.mock('@/api/auth', () => ({ getPublicSettings: vi.fn() }))
vi.mock('@/api/admin/system', () => ({ checkUpdates: vi.fn() }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('../ChannelStatusV1View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV1View', setup: () => () => h('div', { 'data-testid': 'v1' }) }),
}))
vi.mock('../ChannelStatusV2View.vue', () => ({
  default: defineComponent({ name: 'ChannelStatusV2View', setup: () => () => h('div', { 'data-testid': 'v2' }) }),
}))

import ChannelStatusView from '../ChannelStatusView.vue'

describe('渠道监控按保存的模式展示', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    delete window.__APP_CONFIG__
    vi.mocked(getPublicSettings).mockReset()
    vi.mocked(getPublicSettings).mockResolvedValue({ channel_monitor_mode: 'v2' } as PublicSettings)
    useAppStore().cachedPublicSettings = { channel_monitor_mode: 'v2' } as PublicSettings
  })

  it('V2 设置展示详细统计', async () => {
    const wrapper = mount(ChannelStatusView)
    await flushPromises()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
  })

  it('renders V1 when in v1 mode', async () => {
    vi.mocked(getPublicSettings).mockResolvedValue({ channel_monitor_mode: 'v1' } as PublicSettings)
    useAppStore().cachedPublicSettings = { channel_monitor_mode: 'v1' } as PublicSettings
    const wrapper = mount(ChannelStatusView)
    await flushPromises()
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
  })

  it('设置更新后立即切换页面', async () => {
    const wrapper = mount(ChannelStatusView)
    await flushPromises()
    useAppStore().cachedPublicSettings = { channel_monitor_mode: 'v1' } as PublicSettings
    await nextTick()
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
    useAppStore().cachedPublicSettings = { channel_monitor_mode: 'v2' } as PublicSettings
    await nextTick()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
  })

  it('未注入配置时独立加载设置，不先挂载 V1 或等待 setup 请求', async () => {
    useAppStore().cachedPublicSettings = null
    let resolve!: (settings: PublicSettings) => void
    vi.mocked(getPublicSettings).mockReturnValue(new Promise(done => { resolve = done }))
    const wrapper = mount(ChannelStatusView)
    expect(getPublicSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
    resolve({ channel_monitor_mode: 'v2' } as PublicSettings)
    await flushPromises()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('进入页面重新核对公开设置，旧 V1 缓存不能覆盖线上 V2', async () => {
    const appStore = useAppStore()
    appStore.cachedPublicSettings = { channel_monitor_mode: 'v1' } as PublicSettings
    appStore.publicSettingsLoaded = true
    const wrapper = mount(ChannelStatusView)
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    await flushPromises()
    expect(getPublicSettings).toHaveBeenCalledTimes(1)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('首次配置失败可重试，不挂载默认 V1', async () => {
    useAppStore().cachedPublicSettings = null
    vi.spyOn(useAppStore(), 'fetchPublicSettings').mockResolvedValueOnce(null)
    const wrapper = mount(ChannelStatusView)
    await flushPromises()
    expect(wrapper.find('[data-testid="v1"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('刷新失败时允许使用已有配置，不永久卡住加载', async () => {
    vi.spyOn(useAppStore(), 'fetchPublicSettings').mockResolvedValueOnce(null)
    const wrapper = mount(ChannelStatusView)
    await flushPromises()
    expect(wrapper.find('[data-testid="v2"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
