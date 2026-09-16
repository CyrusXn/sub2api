import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AdminQuickActions from '../AdminQuickActions.vue'
import { clampQuickActionPosition, defaultAdminQuickActions, isSafeQuickActionURL, normalizeAdminQuickActions } from '@/utils/adminQuickActions'

const { auth, settings } = vi.hoisted(() => ({
  auth: { isAdmin: true },
  settings: { adminQuickActions: [] as { name: string; url: string }[], fetch: vi.fn() }
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => settings }))

async function mountActions() {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push('/admin/dashboard')
  return mount(AdminQuickActions, { global: { plugins: [router], stubs: { Teleport: true } } })
}

describe('管理员共用快捷菜单', () => {
  beforeEach(() => {
    localStorage.clear()
    auth.isAdmin = true
    settings.adminQuickActions = defaultAdminQuickActions()
    settings.fetch.mockClear()
  })
  it('缺省提供五个菜单，清空保持清空，拒绝危险 URL', () => {
    expect(normalizeAdminQuickActions(undefined)).toHaveLength(5)
    expect(normalizeAdminQuickActions([])).toEqual([])
    for (const url of ['javascript:alert(1)', '//evil.test', '/\\evil.test', 'https://u:p@example.com', 'data:text/html,a']) expect(isSafeQuickActionURL(url)).toBe(false)
    expect(isSafeQuickActionURL('/admin/users?status=active')).toBe(true)
    expect(isSafeQuickActionURL('https://example.com/docs')).toBe(true)
    expect(clampQuickActionPosition(-100, 999, 375, 667)).toEqual({ x: 8, y: 603 })
  })
  it('普通用户不显示也不请求管理员设置', async () => {
    auth.isAdmin = false
    const wrapper = await mountActions()
    expect(wrapper.find('button').exists()).toBe(false)
    expect(settings.fetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('展开导航，拖拽限制边界且不会误触展开', async () => {
    const wrapper = await mountActions()
    const button = wrapper.get('button')
    Object.assign(button.element, { setPointerCapture: vi.fn() })
    button.element.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }))
    await nextTick()
    expect(wrapper.find('nav').exists()).toBe(true)
    expect(wrapper.findAll('nav a')).toHaveLength(5)
    await button.trigger('pointerdown', { isPrimary: true, button: 0, pointerId: 1, clientX: 500, clientY: 400 })
    await button.trigger('pointermove', { pointerId: 1, clientX: -1000, clientY: -1000 })
    await button.trigger('pointerup', { pointerId: 1 })
    button.element.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }))
    await nextTick()
    expect(wrapper.find('nav').exists()).toBe(false)
    expect(JSON.parse(localStorage.getItem('admin_quick_actions_position')!)).toEqual({ x: 8, y: 8 })
    button.element.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 0 }))
    await nextTick()
    expect(wrapper.find('nav').exists()).toBe(true)
    await button.trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('nav').exists()).toBe(false)
    wrapper.unmount()
  })
})
