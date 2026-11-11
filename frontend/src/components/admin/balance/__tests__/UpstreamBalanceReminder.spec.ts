import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import UpstreamBalanceReminder from '../UpstreamBalanceReminder.vue'
import type { UpstreamSiteBalanceReminderEvent } from '@/api/admin/balanceCenter'

const subscribeMock = vi.hoisted(() => vi.fn())

vi.mock('@/api/admin/balanceCenter', () => ({
  subscribeUpstreamSiteBalanceReminders: subscribeMock,
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }),
  }
})

const reminder = (host: string, delta: number) => ({
  host,
  display_name: host,
  old_balance: 10,
  new_balance: 10 + delta,
  delta,
  currency: 'USD',
  detected_at: '2026-11-11T00:00:00.000Z',
  version: 1,
})

function emit(event: UpstreamSiteBalanceReminderEvent) {
  const callback = subscribeMock.mock.calls[0]?.[0] as ((value: UpstreamSiteBalanceReminderEvent) => void) | undefined
  expect(callback).toBeTypeOf('function')
  callback?.(event)
}

describe('UpstreamBalanceReminder', () => {
  beforeEach(() => {
    subscribeMock.mockReset()
    subscribeMock.mockReturnValue(vi.fn())
  })

  afterEach(() => vi.clearAllMocks())

  it('restores snapshot, tracks multiple sites, and resolves only the matching site', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    expect(subscribeMock).toHaveBeenCalledOnce()
    emit({ type: 'snapshot', reminders: [reminder('a.example', 5), reminder('b.example', 8)] })
    await nextTick()

    expect(wrapper.findAll('[data-testid^="upstream-balance-reminder-"]')).toHaveLength(2)
    emit({ type: 'resolved', host: 'a.example' })
    await nextTick()
    expect(wrapper.findAll('[data-testid^="upstream-balance-reminder-"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="upstream-balance-reminder-b.example"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="upstream-balance-reminder-a.example"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('allows collapsing but provides no dismiss action', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    emit({ type: 'upsert', reminder: reminder('a.example', 5) })
    await nextTick()

    const toggle = wrapper.get('button')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(wrapper.findAll('button')).toHaveLength(1)
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(wrapper.find('[data-testid="upstream-balance-reminder-list"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('ignores an older resolution after a newer balance increase', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    emit({ type: 'upsert', reminder: { ...reminder('a.example', 5), version: 3 } })
    emit({ type: 'resolved', host: 'a.example', version: 2 })
    await nextTick()

    expect(wrapper.find('[data-testid="upstream-balance-reminder-a.example"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('ignores an older balance increase after a newer resolution', async () => {
    const wrapper = mount(UpstreamBalanceReminder)
    emit({ type: 'resolved', host: 'a.example', version: 4 })
    emit({ type: 'upsert', reminder: { ...reminder('a.example', 5), version: 3 } })
    await nextTick()

    expect(wrapper.find('[data-testid="upstream-balance-reminder-a.example"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
