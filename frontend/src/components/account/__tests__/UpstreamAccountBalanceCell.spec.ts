import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import type { Account } from '@/types'
import UpstreamAccountBalanceCell from '../UpstreamAccountBalanceCell.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const account = (overrides: Partial<Account> = {}) => ({
  id: 1,
  name: 'VoVo Plus',
  platform: 'openai',
  type: 'apikey',
  status: 'active',
  extra: {},
  ...overrides
} as Account)

describe('UpstreamAccountBalanceCell', () => {
  it('shows the live upstream web account balance', () => {
    const wrapper = mount(UpstreamAccountBalanceCell, {
      props: {
        account: account({
          extra: {
            upstream_billing_probe: {
              status: 'ok',
              last_attempt_at: '2026-07-28T08:00:00Z',
              next_probe_at: '2026-07-28T08:30:00Z',
              balance: {
                status: 'ok',
                amount: 12.3456,
                unit: 'USD',
                last_attempt_at: '2026-07-28T08:00:00Z'
              }
            }
          }
        })
      }
    })

    expect(wrapper.get('[data-test="upstream-account-balance"]').text()).toBe('12.35 USD')
  })

  it('shows a clear unconfigured state when no shared web credential exists', () => {
    const wrapper = mount(UpstreamAccountBalanceCell, {
      props: {
        account: account({
          extra: {
            upstream_billing_probe: {
              status: 'failed',
              last_attempt_at: '2026-07-28T08:00:00Z',
              next_probe_at: '2026-07-28T08:30:00Z',
              last_error: 'missing_web_login_credentials'
            }
          }
        })
      }
    })

    expect(wrapper.text()).toContain('admin.accounts.upstreamBalance.notConfigured')
  })

  it('does not show a balance for unsupported account types', () => {
    const wrapper = mount(UpstreamAccountBalanceCell, {
      props: { account: account({ platform: 'anthropic', type: 'oauth' }) }
    })

    expect(wrapper.text()).toBe('-')
  })
})
