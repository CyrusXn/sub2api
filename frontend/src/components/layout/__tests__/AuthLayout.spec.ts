import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AuthLayout from '@/components/layout/AuthLayout.vue'

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    siteName: '测试平台',
    siteLogo: '',
    cachedPublicSettings: null,
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
  }),
}))

describe('AuthLayout', () => {
  it('默认副标题和版权提示均使用中文', () => {
    const wrapper = mount(AuthLayout)

    expect(wrapper.text()).toContain('API 订阅转换平台')
    expect(wrapper.text()).toContain('版权所有')
    expect(wrapper.text()).not.toContain('All rights reserved')
  })
})
