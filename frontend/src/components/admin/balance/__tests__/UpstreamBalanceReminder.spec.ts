import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import UpstreamBalanceReminder from '../UpstreamBalanceReminder.vue'

const api = vi.hoisted(() => ({
  pendingRechargeReviews: vi.fn(),
  rechargeReviewPosition: vi.fn(),
  saveRechargeReviewPosition: vi.fn(),
  updatePendingRechargeAmount: vi.fn(),
  confirmPendingRecharge: vi.fn(),
  confirmAllPendingRecharges: vi.fn(),
  subscribe: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin/balanceCenter', () => ({
  balanceCenterAPI: {
    pendingRechargeReviews: api.pendingRechargeReviews,
    rechargeReviewPosition: api.rechargeReviewPosition,
    saveRechargeReviewPosition: api.saveRechargeReviewPosition,
    updatePendingRechargeAmount: api.updatePendingRechargeAmount,
    confirmPendingRecharge: api.confirmPendingRecharge,
    confirmAllPendingRecharges: api.confirmAllPendingRecharges
  },
  subscribeUpstreamSiteBalanceReminders: api.subscribe
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.showError, showSuccess: api.showSuccess }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ locale: { value: 'zh' }, t: (key: string) => key }) }
})

const recharge = { id: 5, source: 'upstream_auto', source_key: 'a.example:stamp', site_label: '站点 A', amount: 8, currency: 'CNY', occurred_at: '2026-11-11T00:00:00.000Z', note: '', record_type: 'recharge' as const, review_status: 'pending' as const }

describe('UpstreamBalanceReminder', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.pendingRechargeReviews.mockResolvedValue({ items: [recharge], total: 1 })
    api.rechargeReviewPosition.mockResolvedValue(null)
    api.saveRechargeReviewPosition.mockResolvedValue(undefined)
    api.subscribe.mockReturnValue(vi.fn())
  })
  afterEach(() => vi.clearAllMocks())

  it('shows only the review count when collapsed and preserves detected time when expanded', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    const reminder = wrapper.get('[data-testid="upstream-balance-reminders"]')
    const toggle = reminder.get('button')
    expect(reminder.find('#upstream-balance-reminder-list').exists()).toBe(false)
    const dot = toggle.get('[data-testid="collapsed-review-dot"]')
    expect(dot.attributes('aria-hidden')).toBe('true')
    expect(dot.find('.review-dot-orbit').exists()).toBe(true)
    expect(toggle.text()).toBe('1')
    expect(toggle.attributes('aria-label')).toContain('admin.balanceCenter.reminder.expand')
    expect(reminder.attributes('style')).toContain('top: 80px')
    await toggle.trigger('click')
    expect(toggle.find('.review-dot-orbit').exists()).toBe(false)
    expect(toggle.text()).toContain('admin.balanceCenter.reminder.title')
    expect(wrapper.get('time').attributes('datetime')).toBe(recharge.occurred_at)
    await toggle.trigger('click')
    expect(toggle.text()).toBe('1')
    expect(reminder.find('#upstream-balance-reminder-list').exists()).toBe(false)
    wrapper.unmount()
  })

  it('restores the server position, saves only at the end of a drag and does not open the list', async () => {
    api.rechargeReviewPosition.mockResolvedValue({ x: 0, y: 0 })
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    const badge = wrapper.get('aside > button')
    const reminder = wrapper.get('aside')
    expect(reminder.attributes('style')).toContain('left: 16px')
    expect(reminder.attributes('style')).toContain('top: 16px')
    await badge.trigger('pointerdown', { pointerId: 7, button: 0, clientX: 20, clientY: 20 })
    await badge.trigger('pointermove', { pointerId: 7, clientX: 120, clientY: 170 })
    expect(reminder.attributes('style')).toContain('left: 116px')
    expect(api.saveRechargeReviewPosition).not.toHaveBeenCalled()
    await badge.trigger('pointerup', { pointerId: 7 })
    badge.element.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }))
    await flushPromises()
    expect(api.saveRechargeReviewPosition).toHaveBeenCalledOnce()
    expect(api.saveRechargeReviewPosition).toHaveBeenCalledWith({ x: 100 / (innerWidth - 64), y: 150 / (innerHeight - 64) })
    expect(wrapper.find('#upstream-balance-reminder-list').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not save a canceled drag and restores the previous position', async () => {
    api.rechargeReviewPosition.mockResolvedValue({ x: 0, y: 0 })
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    const badge = wrapper.get('aside > button')
    await badge.trigger('pointerdown', { pointerId: 7, button: 0, clientX: 20, clientY: 20 })
    await badge.trigger('pointermove', { pointerId: 7, clientX: 120, clientY: 170 })
    await badge.trigger('pointercancel', { pointerId: 7 })
    expect(wrapper.get('aside').attributes('style')).toContain('left: 16px')
    expect(api.saveRechargeReviewPosition).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('keeps the badge in the viewport and restores its last position when saving fails', async () => {
    api.rechargeReviewPosition.mockResolvedValue({ x: 0, y: 0 })
    api.saveRechargeReviewPosition.mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    const badge = wrapper.get('aside > button')
    await badge.trigger('pointerdown', { pointerId: 7, button: 0, clientX: 20, clientY: 20 })
    await badge.trigger('pointermove', { pointerId: 7, clientX: innerWidth + 100, clientY: innerHeight + 100 })
    expect(wrapper.get('aside').attributes('style')).toContain(`left: ${innerWidth - 48}px`)
    await badge.trigger('pointerup', { pointerId: 7 })
    await flushPromises()
    expect(api.saveRechargeReviewPosition).toHaveBeenCalledWith({ x: 1, y: 1 })
    expect(wrapper.get('aside').attributes('style')).toContain('left: 16px')
    expect(api.showError).toHaveBeenCalledWith('admin.balanceCenter.reminder.positionSaveError')
    wrapper.unmount()
  })

  it('saves an edited amount before confirming and removes confirmed rows', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    await wrapper.get('aside > button').trigger('click')
    expect(wrapper.findAll('[data-testid="recharge-review-5"] button')).toHaveLength(1)
    await wrapper.get('input[type="number"]').setValue('12')
    await wrapper.get('[data-testid="recharge-review-5"] button:last-child').trigger('click')
    await flushPromises()
    expect(api.updatePendingRechargeAmount).not.toHaveBeenCalled()
    expect(api.confirmPendingRecharge).toHaveBeenCalledWith(5, 12)
    expect(wrapper.find('[data-testid="upstream-balance-reminders"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('confirms all with unsaved edits, including after a reminder refresh', async () => {
    api.pendingRechargeReviews.mockResolvedValue({ items: [recharge, { ...recharge, id: 6 }], total: 2 })
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    await wrapper.get('aside > button').trigger('click')
    await wrapper.get('[data-testid="recharge-review-5"] input').setValue('200')
    api.subscribe.mock.calls[0][0]({ type: 'upsert' })
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-testid="recharge-review-5"] input').element.value).toBe('200')
    api.pendingRechargeReviews.mockResolvedValue({ items: [], total: 0 })
    await wrapper.get('#upstream-balance-reminder-list > div button').trigger('click')
    await flushPromises()
    expect(api.confirmAllPendingRecharges).toHaveBeenCalledOnce()
    expect(api.confirmAllPendingRecharges).toHaveBeenCalledWith([{ id: 5, amount: 200 }])
    expect(wrapper.find('[data-testid="upstream-balance-reminders"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not confirm anything when an amount is invalid', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    await wrapper.get('aside > button').trigger('click')
    await wrapper.get('input').setValue('')
    await wrapper.get('[data-testid="recharge-review-5"] button:last-child').trigger('click')
    await wrapper.get('#upstream-balance-reminder-list > div button').trigger('click')
    expect(api.confirmPendingRecharge).not.toHaveBeenCalled()
    expect(api.confirmAllPendingRecharges).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="recharge-review-5"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
