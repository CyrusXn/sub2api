import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BusinessHistoryView from '../BusinessHistoryView.vue'
import zhBusinessHistory from '@/i18n/locales/zh/admin/businessHistory'
import enBusinessHistory from '@/i18n/locales/en/admin/businessHistory'

const getBusinessSummary = vi.hoisted(() => vi.fn())
const assets = vi.hoisted(() => vi.fn())
const rechargeSummary = vi.hoisted(() => vi.fn())
const notifications = vi.hoisted(() => ({ showError: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { dashboard: { getBusinessSummary } } }))
vi.mock('@/api/admin/balanceCenter', () => ({ balanceCenterAPI: { assets, rechargeSummary } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('vue-chartjs', () => ({
  Line: {
    name: 'Line',
    props: ['data', 'options'],
    template: '<div data-test="business-history-chart" />'
  }
}))

const response = {
  profit_history: [
    { bucket_date: '2026-08-16', cumulative_profit: 13081.93, daily_profit: null, captured_at: '2026-08-16T15:59:30Z', status: 'snapshot' },
    { bucket_date: '2026-08-17', cumulative_profit: 13581.93, daily_profit: 500, captured_at: '2026-08-17T02:00:00Z', status: 'current' }
  ],
  // 实账样例故意与请求估算不同，防止卡片退回估算口径。
  ledger: {
    cash_balance: 600, subscription_balance: 78.9, total_balance: 678.9,
    known_balance_subtotal: 678.9, unknown_sites: 0,
    lifetime_consumption_estimate: 8254.07, lifetime_profit_estimate: -8174.07,
    opening_balance: 700, closing_balance: 660.25,
    opening_captured_at: '2026-07-08T15:59:00Z', closing_captured_at: '2026-08-17T02:00:00Z',
    range_consumption: 1274.31, range_profit: -1254.31, range_status: 'available'
  },
  upstream_recharge_total: 8932.97,
  user_balance_total: 321.45,
  upstream_balance_total: 678.9,
  range_upstream_recharge_total: 1234.56,
  range_user_balance_total: 300.5,
  range_upstream_balance_total: 660.25,
  range_balance_snapshot_date: '2026-08-16T00:00:00Z',
  lifetime: {
    recharge_amount: 500,
    total_requests: 999,
    input_tokens: 1000,
    output_tokens: 200,
    cache_creation_tokens: 100,
    cache_read_tokens: 700,
    total_tokens: 2000,
    total_cost: 90,
    actual_cost: 80,
    actual_cost_excluding_admin: 70,
    account_cost: 60,
    account_cost_excluding_admin: 50,
    upstream_cost: 45,
    upstream_cost_excluding_admin: 40
  },
  range: {
    recharge_amount: 120,
    total_requests: 123,
    input_tokens: 1000,
    output_tokens: 200,
    cache_creation_tokens: 100,
    cache_read_tokens: 700,
    total_tokens: 2000,
    total_cost: 30,
    actual_cost: 20,
    actual_cost_excluding_admin: 18,
    account_cost: 10,
    account_cost_excluding_admin: 9,
    upstream_cost: 8,
    upstream_cost_excluding_admin: 7
  },
  daily: [
    { bucket_date: '2026-08-17T00:00:00Z', recharge_amount: 70, total_requests: 73, total_tokens: 1200, actual_cost: 12, actual_cost_excluding_admin: 11, account_cost: 7, account_cost_excluding_admin: 6, upstream_cost: 5, upstream_cost_excluding_admin: 4 },
    { bucket_date: '2026-08-16T00:00:00Z', recharge_amount: 50, total_requests: 50, total_tokens: 800, actual_cost: 8, actual_cost_excluding_admin: 7, account_cost: 3, account_cost_excluding_admin: 3, upstream_cost: 3, upstream_cost_excluding_admin: 3 }
  ]
}

const mountView = () => mount(BusinessHistoryView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      Icon: true,
      LoadingSpinner: true,
      BaseDialog: { props: ['show', 'title'], template: '<div v-if="show" data-test="details-dialog"><h2>{{ title }}</h2><slot /></div>' }
    }
  }
})

describe('BusinessHistoryView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-17T10:00:00+08:00'))
    getBusinessSummary.mockReset()
    getBusinessSummary.mockResolvedValue(response)
    notifications.showError.mockReset()
    assets.mockReset()
    rechargeSummary.mockReset()
  })

  afterEach(() => vi.useRealTimers())

  it('默认从 2026 年 7 月 9 日查询并展示范围内经营指标与全部上游充值总额', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(getBusinessSummary).toHaveBeenCalledWith({ start_date: '2026-07-09', end_date: '2026-08-17' })
    expect(wrapper.get('[data-test="range-requests"]').text()).toContain('123')
    expect(wrapper.get('[data-test="range-tokens"]').text()).toContain('2.00K')
    expect(wrapper.get('[data-test="range-consumption"]').text()).toContain('¥80.00')
    expect(wrapper.get('[data-test="range-consumption"]').text()).toContain('¥70.00')
    expect(wrapper.get('[data-test="user-total-recharge"]').text()).toBe('¥120.00')
    expect(wrapper.get('[data-test="user-balance-total"]').text()).toBe('¥300.50')
    expect(wrapper.get('[data-test="upstream-recharge-total"]').text()).toBe('¥8,932.97')
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥678.90')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8,254.07')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-8,174.07')
    expect(wrapper.get('[data-test="ledger-lifetime-consumption"]').text()).toBe('¥8,254.07')
    expect(wrapper.get('[data-test="user-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.balanceSnapshotAsOf')
    expect(wrapper.get('[data-test="upstream-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.currentBalanceHint')
    expect(wrapper.get('[data-test="token-breakdown"]').text()).toContain('1.00K')
    expect(wrapper.get('[data-test="token-breakdown"]').text()).toContain('700')
  })

  it('明确标注排除管理员后的消费口径', () => {
    expect(zhBusinessHistory.businessHistory.excludingAdmin).toBe("消费总额（已排除 admin{'@'}example.com）")
    expect(enBusinessHistory.businessHistory.excludingAdmin).toBe("消费总额（已排除 admin{'@'}example.com）")
    expect(zhBusinessHistory.businessHistory.userTotalRecharge).toBe('用户总充值')
    expect(zhBusinessHistory.businessHistory.upstreamRechargeTotal).toBe('上游总充值')
    expect(enBusinessHistory.businessHistory.upstreamRechargeTotal).toBe('上游总充值')
  })

  it('不再显示与页面标题重复的经营历史说明', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).not.toContain('按日期查询永久保存的请求数、Token 和消费汇总')
    expect(wrapper.find('h1').exists()).toBe(false)
  })

  it('复用 AppLayout 的页面内边距', async () => {
    const wrapper = mountView()
    await flushPromises()

    const content = wrapper.get('[data-test="business-history-content"]')
    expect(content.classes()).toContain('space-y-6')
    expect(content.classes().some((className) => /^(?:sm:)?p[xy]-/.test(className))).toBe(false)
  })

  it('按管理员选择的日期范围重新查询', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="history-start-date"]').setValue('2026-08-01')
    await wrapper.get('[data-test="history-end-date"]').setValue('2026-08-10')
    await wrapper.get('[data-test="history-query"]').trigger('click')
    await flushPromises()

    expect(getBusinessSummary).toHaveBeenLastCalledWith({ start_date: '2026-08-01', end_date: '2026-08-10' })
  })

  it('每日明细倒序、趋势图升序，累计收益读取全部历史账本口径', async () => {
    const wrapper = mountView()
    await flushPromises()

    const rows = wrapper.findAll('[data-test="history-daily-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('2026-08-17')
    expect(rows[1].text()).toContain('2026-08-16')
    expect(rows[0].get('[data-test="history-cumulative-profit"]').text()).toBe('¥13,581.93')
    expect(rows[1].get('[data-test="history-cumulative-profit"]').text()).toBe('¥13,081.93')

    const chart = wrapper.findComponent({ name: 'Line' })
    expect((chart.props('data') as any).labels).toEqual(['2026-08-16', '2026-08-17'])
  })

  it('余额弹窗显示每站现金与订阅合计，未知资产不能计为零', async () => {
    assets.mockResolvedValue([
      { site_id: 1, site_name: '站点甲', domain: 'a.example.com', cash_balance: 10, subscription_balance: 20, total_balance: 30, balance_known: true },
      { site_id: 2, site_name: '站点乙', domain: 'b.example.com', cash_balance: 5, subscription_balance: null, total_balance: null, balance_known: false }
    ])
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="open-balance-details"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-test="balance-detail-row"]')).toHaveLength(2)
    expect(wrapper.get('[data-test="balance-detail-total"]').text()).toBe('--')
  })

  it('充值弹窗不带日期筛选，按站点列出充值记录', async () => {
    rechargeSummary.mockResolvedValue({ total_amount: 80, sites: [{ site_id: 1, site_name: '站点甲', total_amount: 80, items: [{ id: 1, occurred_at: '2026-08-16T01:00:00Z', amount: 80, record_type: 'recharge', note: '余额增量自动识别' }] }] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="open-recharge-details"]').trigger('click')
    await flushPromises()
    expect(rechargeSummary).toHaveBeenCalledWith({})
    expect(wrapper.get('[data-test="recharge-detail-row"]').text()).toContain('¥80.00')
    expect(wrapper.get('[data-test="details-dialog"]').text()).toContain('站点甲')
  })

  it('总收益弹窗倒序展示当天与截至当天累计，缺失快照显示空值', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="open-profit-details"]').trigger('click')
    const rows = wrapper.findAll('[data-test="profit-detail-row"]')
    expect(rows[0].text()).toContain('2026-08-17')
    expect(rows[0].text()).toContain('¥500.00')
    expect(rows[0].text()).toContain('¥13,581.93')
    expect(rows[1].text()).toContain('--')
  })

  it('历史边界缺失不阻止累计公式，当前余额仍参与计算', async () => {
    getBusinessSummary.mockResolvedValue({
      ...response,
      range_user_balance_total: null,
      range_upstream_balance_total: null,
      range_balance_snapshot_date: null,
      ledger: { ...response.ledger, opening_balance: null, closing_balance: null, range_consumption: null, range_profit: null, range_status: 'missing_boundary' }
    })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8,254.07')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-8,174.07')
    expect(wrapper.get('[data-test="user-balance-total"]').text()).toBe('--')
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥678.90')
    expect(wrapper.get('[data-test="user-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.balanceSnapshotEmpty')
  })

  it('收益趋势同图展示全部账号与排除 admin 两条线', async () => {
    const wrapper = mountView()
    await flushPromises()

    const profitButton = wrapper.findAll('button').find((button) => button.text() === 'admin.businessHistory.profitTrend')
    expect(profitButton).toBeTruthy()
    await profitButton!.trigger('click')

    const chart = wrapper.findComponent({ name: 'Line' })
    const datasets = (chart.props('data') as any).datasets
    expect(datasets).toHaveLength(2)
    expect(datasets[0].label).toBe('admin.businessHistory.profitLegendAll')
    expect(datasets[0].data).toEqual([5, 7])
    expect(datasets[1].label).toBe('admin.businessHistory.profitLegendExcludingAdmin')
    expect(datasets[1].data).toEqual([4, 7])
    expect((chart.props('options') as any).plugins.legend.display).toBe(true)
  })

  it('没有每日汇总时显示历史暂无统计数据', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, daily: [] })
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.businessHistory.noData')
    expect(wrapper.find('[data-test="business-history-chart"]').exists()).toBe(false)
  })
})
