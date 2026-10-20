import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const copyToClipboard = vi.fn().mockResolvedValue(true)

const messages: Record<string, string> = {
  'keys.endpoints.title': 'API 端点',
  'keys.endpoints.default': '默认',
  'keys.endpoints.copied': '已复制',
  'keys.endpoints.copiedHint': '已复制到剪贴板',
  'keys.endpoints.clickToCopy': '点击可复制此端点',
  'keys.endpoints.speedTest': '测速',
}

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => messages[key] ?? key,
  }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard,
  }),
}))

import EndpointPopover from '../EndpointPopover.vue'

describe('EndpointPopover', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('直连与默认端点使用相同标题和绿色标签，随后展示地址', () => {
    const wrapper = mount(EndpointPopover, {
      props: {
        apiBaseUrl: 'https://api.example.com',
        customEndpoints: [{ name: '直连', endpoint: 'https://ai.example.com', description: '直连服务器' }],
      },
    })
    const titles = wrapper.findAll('span.font-medium.text-gray-600')
    expect(titles.map(item => item.text())).toEqual(['API 端点', 'API 端点'])
    const badges = wrapper.findAll('span.text-primary-600')
    expect(badges.map(item => item.text())).toEqual(['默认', '直连'])
    expect(badges[1].classes()).toEqual(badges[0].classes())
    expect(wrapper.findAll('code').map(item => item.text())).toEqual(['https://api.example.com', 'https://ai.example.com'])
  })

  it('将说明提示渲染到 URL 上方而不是旧的 title 图标上', () => {
    const wrapper = mount(EndpointPopover, {
      props: {
        apiBaseUrl: 'https://default.example.com/v1',
        customEndpoints: [
          {
            name: '备用线路',
            endpoint: 'https://backup.example.com/v1',
            description: '自定义说明',
          },
        ],
      },
    })

    expect(wrapper.text()).toContain('自定义说明')
    expect(wrapper.text()).toContain('点击可复制此端点')
    expect(wrapper.find('[role="button"]').attributes('title')).toBeUndefined()
    expect(wrapper.find('[title="自定义说明"]').exists()).toBe(false)
  })

  it('点击 URL 后会复制并切换为已复制提示', async () => {
    const wrapper = mount(EndpointPopover, {
      props: {
        apiBaseUrl: 'https://default.example.com/v1',
        customEndpoints: [],
      },
    })

    await wrapper.find('[role="button"]').trigger('click')
    await flushPromises()

    expect(copyToClipboard).toHaveBeenCalledWith('https://default.example.com/v1', '已复制')
    expect(wrapper.text()).toContain('已复制到剪贴板')
    expect(wrapper.find('button[aria-label="已复制到剪贴板"]').exists()).toBe(true)
  })
})
