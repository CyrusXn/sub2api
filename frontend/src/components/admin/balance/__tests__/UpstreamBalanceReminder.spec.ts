import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import UpstreamBalanceReminder from '../UpstreamBalanceReminder.vue'

const api = vi.hoisted(() => ({
  pendingRechargeReviews: vi.fn(),
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
    api.subscribe.mockReturnValue(vi.fn())
  })
  afterEach(() => vi.clearAllMocks())

  it('shows a floating review count, hides the list until expanded, and preserves detected time', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    expect(wrapper.find('[data-testid="upstream-balance-reminders"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="upstream-balance-reminder-list"]').exists()).toBe(false)
    await wrapper.get('aside > button').trigger('click')
    expect(wrapper.get('time').attributes('datetime')).toBe(recharge.occurred_at)
    wrapper.unmount()
  })

  it('saves an edited amount before confirming and removes confirmed rows', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    await wrapper.get('aside > button').trigger('click')
    await wrapper.get('input[type="number"]').setValue('12')
    await wrapper.get('[data-testid="recharge-review-5"] button:last-child').trigger('click')
    await flushPromises()
    expect(api.updatePendingRechargeAmount).toHaveBeenCalledWith(5, 12)
    expect(api.confirmPendingRecharge).toHaveBeenCalledWith(5)
    expect(wrapper.find('[data-testid="upstream-balance-reminders"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('confirms all and hides the reminder when the queue is empty', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    await flushPromises()
    await wrapper.get('aside > button').trigger('click')
    await wrapper.get('#upstream-balance-reminder-list > div button').trigger('click')
    await flushPromises()
    expect(api.confirmAllPendingRecharges).toHaveBeenCalledOnce()
    expect(wrapper.find('[data-testid="upstream-balance-reminders"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
