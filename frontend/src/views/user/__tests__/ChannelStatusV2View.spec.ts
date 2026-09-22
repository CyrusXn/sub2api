import { flushPromises, shallowMount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelStatusV2View from '../ChannelStatusV2View.vue'
import FilterMultiSelect from '@/features/channel-monitor-v2/FilterMultiSelect.vue'
import * as api from '@/api/channelMonitorV2'

const route = vi.hoisted(() => ({ query: {} as Record<string, string> }))
const replace = vi.hoisted(() => vi.fn())
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, te: () => true, locale: { value: 'zh' } }),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/api/channelMonitorV2', () => ({
  getDimensions: vi.fn().mockResolvedValue({ platforms: [], groups: [], models: [] }),
  getSnapshot: vi.fn().mockResolvedValue(null),
  getMatrix: vi.fn().mockResolvedValue({ items: [], coverage: {} }),
  getModels: vi.fn().mockResolvedValue({ items: [] }),
}))

let wrapper: ReturnType<typeof shallowMount> | undefined
beforeEach(() => {
  route.query = {}
  vi.clearAllMocks()
})
afterEach(() => wrapper?.unmount())

async function openPage() {
  wrapper = shallowMount(ChannelStatusV2View, {
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' } } },
  })
  await flushPromises()
  return wrapper
}

describe('渠道监控默认平台', () => {
  it('首次打开默认请求 OpenAI，手动选全部后刷新仍保留全部', async () => {
    const page = await openPage()
    expect(api.getSnapshot).toHaveBeenLastCalledWith(
      expect.objectContaining({ platforms: ['openai'] }), true, expect.any(AbortSignal),
    )
    page.findAllComponents(FilterMultiSelect)[0]!.vm.$emit('update:modelValue', [])
    await flushPromises()
    expect(api.getSnapshot).toHaveBeenLastCalledWith(
      expect.objectContaining({ platforms: [] }), true, expect.any(AbortSignal),
    )
    expect(replace).toHaveBeenLastCalledWith(expect.objectContaining({
      query: expect.objectContaining({ platform: '' }),
    }))
    route.query = replace.mock.lastCall![0].query
    page.unmount()
    await openPage()
    expect(api.getSnapshot).toHaveBeenLastCalledWith(
      expect.objectContaining({ platforms: [] }), true, expect.any(AbortSignal),
    )
  })

  it('保留链接指定的其他平台', async () => {
    route.query = { platform: 'anthropic,grok' }
    await openPage()
    expect(api.getSnapshot).toHaveBeenLastCalledWith(
      expect.objectContaining({ platforms: ['anthropic', 'grok'] }), true, expect.any(AbortSignal),
    )
  })
})
