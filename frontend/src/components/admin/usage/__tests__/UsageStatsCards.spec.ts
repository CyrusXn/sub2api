import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import UsageStatsCards from '../UsageStatsCards.vue'

const messages: Record<string, string> = {
  'usage.totalRequests': 'Total Requests',
  'usage.inSelectedRange': 'in selected range',
  'usage.totalTokens': 'Total Tokens',
  'usage.upstreamTotalTokens': 'Upstream Total Tokens',
  'usage.originalTokenCount': 'Original token count',
  'usage.in': 'In',
  'usage.out': 'Out',
  'usage.cacheTotal': 'Cache',
  'usage.cacheBreakdown': 'Cache Token Breakdown',
  'usage.cacheCreationTokensLabel': 'Cache Creation',
  'usage.cacheReadTokensLabel': 'Cache Read',
  'usage.totalCost': 'Total Cost',
  'usage.upstreamTotalCost': 'Upstream Total Cost',
  'usage.upstreamCostSnapshot': 'Settled upstream snapshot',
  'usage.profit': 'Profit',
  'usage.profitFormula': 'Total cost - upstream cost',
  'usage.accountCost': 'Cost',
  'usage.standardCost': 'Standard',
  'usage.avgDuration': 'Avg Duration',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const stats = {
  total_requests: 1,
  total_input_tokens: 100,
  total_output_tokens: 50,
  total_cache_tokens: 34,
  total_cache_creation_tokens: 12,
  total_cache_read_tokens: 22,
  total_tokens: 184,
  total_cost: 0.001,
  total_actual_cost: 0.001,
  total_account_cost: 0.001,
  total_upstream_cost: 0.00025,
  average_duration_ms: 250,
}

describe('UsageStatsCards', () => {
  it('shows cache token breakdown values', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('Cache: 34')
    expect(text).toContain('Cache Token Breakdown')
    expect(text).toContain('Cache Creation')
    expect(text).toContain('12')
    expect(text).toContain('Cache Read')
    expect(text).toContain('22')
    expect(text).toContain('Total Requests')
    expect(text).toContain('Total Tokens')
    expect(text).not.toContain('Upstream Total Tokens')
    expect(text).not.toContain('Upstream Total Cost')
    expect(text).not.toContain('Profit')
  })

  it('shows upstream cost and profit only for admin metrics', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats: { ...stats, total_actual_cost: 2, total_account_cost: 0.75, total_upstream_cost: 0.25 },
        showUpstreamMetrics: true,
      },
      global: { stubs: { Icon: true } },
    })

    expect(wrapper.text()).toContain('Upstream Total Tokens')
    expect(wrapper.text()).toContain('Original token count')
    expect(wrapper.text()).toContain('184')
    expect(wrapper.text()).toContain('$0.2500')
    expect(wrapper.text()).toContain('$1.7500')
  })

  it('keeps the cache tooltip out of the layout while it is hidden', () => {
    const wrapper = mount(UsageStatsCards, {
      props: {
        stats,
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    const tooltip = wrapper.findAll('span').find((el) => el.classes().includes('group-hover:block'))

    expect(tooltip).toBeDefined()
    // `opacity-0` hides the tooltip visually but keeps it in the layout, so its
    // fixed width still widens the document and causes horizontal scrolling on
    // narrow screens. `hidden` (display: none) takes it out of the flow.
    expect(tooltip?.classes()).toContain('hidden')
    expect(tooltip?.classes()).not.toContain('opacity-0')
  })
})
