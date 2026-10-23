import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import BusinessHistoryView from '../BusinessHistoryView.vue'
import { ref } from 'vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import zhBusinessHistory from '@/i18n/locales/zh/admin/businessHistory'
import enBusinessHistory from '@/i18n/locales/en/admin/businessHistory'

const getBusinessSummary = vi.hoisted(() => vi.fn())
const assets = vi.hoisted(() => vi.fn())
const rechargeSummary = vi.hoisted(() => vi.fn())
const refreshBusinessBalances = vi.hoisted(() => vi.fn())
const notifications = vi.hoisted(() => ({ showError: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { dashboard: { getBusinessSummary }, accounts: { refreshBusinessBalances } } }))
vi.mock('@/api/admin/balanceCenter', () => ({ balanceCenterAPI: { assets, rechargeSummary } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key, locale: ref('zh') })
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
    { bucket_date: '2026-08-17', cumulative_profit: 13581.93, daily_profit: 500, captured_at: '2026-08-17T02:00:00Z', status: 'current' },
    { bucket_date: '2026-07-08', cumulative_profit: 10, daily_profit: 1, captured_at: '2026-07-08T02:00:00Z', status: 'snapshot' }
  ],
  // 实账样例故意与请求估算不同，防止卡片退回估算口径。
  ledger: {
    cash_balance: 600, subscription_balance: 78.9, total_balance: 678.9,
    known_balance_subtotal: 678.9, unknown_sites: 0,
    lifetime_consumption_estimate: 8254.07, lifetime_profit_estimate: -8174.07,
    opening_balance: 600, closing_balance: 660.25,
    opening_captured_at: '2026-07-08T15:59:00Z', closing_captured_at: '2026-08-17T02:00:00Z',
    range_consumption: null, range_profit: null, range_status: 'inexact_boundary',
    range_consumption_estimate: 1174.31, range_profit_estimate: -1154.31
  },
  upstream_recharge_total: 8932.97,
  user_balance_total: 321.45,
  upstream_balance_total: 678.9,
  range_upstream_recharge_total: 1234.56,
  range_user_balance_total: 300.5,
  range_upstream_balance_total: 660.25,
  range_balance_snapshot_date: '2026-08-16T00:00:00Z',
  range_balance_captured_at: '2026-08-16T15:59:30Z',
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

const mountAllView = () => mount(BusinessHistoryView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      Icon: true,
      LoadingSpinner: true,
      BaseDialog: { props: ['show', 'title'], template: '<div v-if="show" data-test="details-dialog"><h2>{{ title }}</h2><slot /></div>' }
    }
  }
})

const mountView = async () => {
  const wrapper = mountAllView()
  await flushPromises()
  return wrapper
}

async function editDates(wrapper: ReturnType<typeof mountAllView>, start: string, end?: string) {
  await wrapper.get('.date-picker-trigger').trigger('click')
  const inputs = wrapper.findAll('.date-picker-input')
  await inputs[0].setValue(start)
  if (end) await inputs[1].setValue(end)
}

async function applyDates(wrapper: ReturnType<typeof mountAllView>, start: string, end: string) {
  await editDates(wrapper, start, end)
  await wrapper.get('.date-picker-apply').trigger('click')
  await flushPromises()
}

describe('BusinessHistoryView', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-08-17T10:00:00+08:00'))
    getBusinessSummary.mockReset()
    getBusinessSummary.mockResolvedValue(response)
    notifications.showError.mockReset()
    assets.mockReset()
    rechargeSummary.mockReset()
    refreshBusinessBalances.mockReset()
    refreshBusinessBalances.mockResolvedValue({ total_balance: 328, failed_accounts: 0, sites: [{ domain: 'up.example', balance: 328, failed_accounts: 0 }], refreshed_at: '2026-08-17T02:00:00Z' })
  })

  afterEach(() => vi.useRealTimers())

  it('默认恢复原日期范围并复用使用记录的日期控件', async () => {
    const wrapper = await mountView()
    expect(getBusinessSummary).toHaveBeenCalledWith({ start_date: '2026-07-09', end_date: '2026-08-17' })
    const picker = wrapper.getComponent(DateRangePicker)
    expect(picker.props('startDate')).toBe('2026-07-09')
    expect(picker.props('endDate')).toBe('2026-08-17')
    expect(wrapper.get('[data-test="applied-range"]').text()).toBe('2026-07-09 ～ 2026-08-17')
  })

  it('日期快捷项仅在应用后查询，经营历史仍按整天核算', async () => {
    const wrapper = await mountView()
    getBusinessSummary.mockClear()
    await wrapper.get('.date-picker-trigger').trigger('click')
    expect(wrapper.findAll('.date-picker-preset').map(button => button.text())).not.toContain('dates.last24Hours')
    await wrapper.findAll('.date-picker-preset').find(button => button.text() === 'dates.lastMonth')!.trigger('click')
    expect(getBusinessSummary).not.toHaveBeenCalled()
    expect(wrapper.findAll('.date-picker-input')[0].attributes('type')).toBe('date')
    await wrapper.get('.date-picker-apply').trigger('click')
    await flushPromises()
    expect(getBusinessSummary).toHaveBeenCalledWith({ start_date: '2026-07-01', end_date: '2026-07-31' })
  })

  it('总余额始终使用账号汇总，全部消费收益保留旧资产账本公式', async () => {
    const wrapper = mountAllView()
    await flushPromises()
    await wrapper.get('[data-test="history-all"]').trigger('click')
    await flushPromises()
    expect(getBusinessSummary).toHaveBeenCalledWith()
    expect(refreshBusinessBalances).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-test="range-requests"]').text()).toBe('999')
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥328.00')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8,254.07')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-8,174.07')
    // 当天累计值使用同一资产账本；历史快照不能被覆盖。
    expect(wrapper.findAll('[data-test="history-cumulative-profit"]')[0].text()).toBe('¥-8,174.07')
    expect(wrapper.findAll('[data-test="history-cumulative-profit"]')[1].text()).toBe('¥13,081.93')
    expect(wrapper.get('[data-test="history-total-cumulative"]').text()).toBe('¥-8,174.07')
    refreshBusinessBalances.mockResolvedValue({ total_balance: 300, failed_accounts: 0, sites: [], refreshed_at: '2026-08-17T02:01:00Z' })
    await wrapper.get('[data-test="refresh-upstream-balance"]').trigger('click')
    await flushPromises()
    expect(refreshBusinessBalances).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥300.00')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-8,174.07')
  })

  it('列表末尾汇总每日数值，累计收益只取最后一天而不重复求和', async () => {
    const wrapper = await mountView()
    const total = wrapper.get('[data-test="history-total-row"]')
    expect(total.text()).toContain('123')
    expect(total.text()).toContain('¥20.00')
    expect(total.text()).toContain('¥12.00')
    expect(total.get('[data-test="history-total-cumulative"]').text()).toBe('¥13,581.93')
  })

  it('账号余额失败按零汇总，卡片与明细正常展示且不影响历史核算', async () => {
    refreshBusinessBalances.mockResolvedValue({ total_balance: 18, failed_accounts: 1, sites: [
      { domain: 'failed.example', balance: 0, failed_accounts: 1 },
      { domain: 'up.example', balance: 18, failed_accounts: 0 }
    ] })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥18.00')
    expect(wrapper.find('[data-test="balance-refresh-error"]').exists()).toBe(false)
    await wrapper.get('[data-test="open-balance-details"]').trigger('click')
    await flushPromises()
    const rows = wrapper.findAll('[data-test="balance-detail-row"]')
    expect(rows[0].text()).toContain('¥0.00')
    expect(rows[0].text()).toContain('admin.businessHistory.balanceAccountsFailed')
    expect(rows[1].text()).toContain('¥18.00')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8.00')
    await wrapper.get('[data-test="history-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥18.00')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8,254.07')
    expect(refreshBusinessBalances).toHaveBeenCalledTimes(1)
    expect(notifications.showError).not.toHaveBeenCalled()
  })

  it('全部资产余额缺失时不拿账号余额或零替代', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: { ...response.ledger, total_balance: null } })
    const wrapper = mountAllView()
    await flushPromises()
    await wrapper.get('[data-test="history-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥328.00')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('--')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('--')
    expect(notifications.showError).not.toHaveBeenCalled()
  })

  it('全部资产余额为零是有效值，仍按旧累计公式计算', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: { ...response.ledger, total_balance: 0 } })
    refreshBusinessBalances.mockResolvedValue({ total_balance: 0, failed_accounts: 0, sites: [], refreshed_at: '' })
    const wrapper = await mountView()
    await wrapper.get('[data-test="history-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥0.00')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8,932.97')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-8,852.97')
  })

  it('余额刷新重新查询全量账号，不提交日期草稿、不重新查询历史', async () => {
    const wrapper = await mountView()
    getBusinessSummary.mockClear()
    await editDates(wrapper, '2026-08-01')
    await wrapper.get('[data-test="refresh-upstream-balance"]').trigger('click')
    await flushPromises()
    expect(getBusinessSummary).not.toHaveBeenCalled()
    expect(refreshBusinessBalances).toHaveBeenCalledTimes(2)
    expect(refreshBusinessBalances).toHaveBeenLastCalledWith()
    expect(wrapper.get('[data-test="applied-range"]').text()).toBe('2026-07-09 ～ 2026-08-17')
  })

  it('切换历史日期不重新探测账号，也不改变总余额', async () => {
    const wrapper = await mountView()
    getBusinessSummary.mockResolvedValue({ ...response, ledger: { ...response.ledger, closing_balance: 10 } })
    await applyDates(wrapper, '2026-08-01', '2026-08-10')
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥328.00')
    expect(refreshBusinessBalances).toHaveBeenCalledTimes(1)
  })

  it('重新获取失败不再展示上轮成功金额，不冒充零余额', async () => {
    const wrapper = await mountView()
    refreshBusinessBalances.mockRejectedValueOnce(new Error('余额读取失败'))
    await wrapper.get('[data-test="refresh-upstream-balance"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('--')
    expect(wrapper.get('[data-test="balance-refresh-error"]').text()).toContain('admin.businessHistory.balanceRefreshFailed')
    expect(wrapper.get('[data-test="refresh-upstream-balance"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥12.00')
  })

  it('余额刷新期间禁用重复刷新，日期查询完成不覆盖余额结果', async () => {
    const wrapper = await mountView()
    let resolveBalance!: (value: unknown) => void
    refreshBusinessBalances.mockReturnValueOnce(new Promise(resolve => { resolveBalance = resolve }))
    await wrapper.get('[data-test="refresh-upstream-balance"]').trigger('click')
    expect(wrapper.get('[data-test="refresh-upstream-balance"]').attributes('disabled')).toBeDefined()
    await applyDates(wrapper, '2026-08-01', '2026-08-10')
    resolveBalance({ total_balance: 200, failed_accounts: 0, sites: [], refreshed_at: '2026-08-17T02:01:00Z' })
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥200.00')
    expect(wrapper.get('[data-test="applied-range"]').text()).toBe('2026-08-01 ～ 2026-08-10')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8.00')
  })

  it('只读预览使用快照时间和提示，不把已有余额标成实时获取', async () => {
    refreshBusinessBalances.mockResolvedValue({ preview_only: true, total_balance: 328, failed_accounts: 0, sites: [], refreshed_at: '2026-08-16T01:00:00Z' })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.balancePreviewSnapshot')
  })

  it('全部余额明细与卡片使用同一批域名最小余额', async () => {
    const wrapper = await mountView()
    await wrapper.get('[data-test="history-all"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="open-balance-details"]').trigger('click')
    await flushPromises()
    expect(assets).not.toHaveBeenCalled()
    const row = wrapper.get('[data-test="balance-detail-row"]')
    expect(row.text()).toContain('up.example')
    expect(row.text()).toContain('¥328.00')
    expect(wrapper.get('[data-test="balance-detail-total"]').text()).toBe('¥328.00')
  })

  it('北京时间跨日时默认结束日不随浏览器当地日期偏移', async () => {
    vi.setSystemTime(new Date('2026-08-17T00:30:00+08:00'))
    const wrapper = await mountView()
    await flushPromises()

    expect(getBusinessSummary).toHaveBeenCalledWith({ start_date: '2026-07-09', end_date: '2026-08-17' })
    wrapper.unmount()
  })

  it('默认日期不变，选定日期消费与使用记录一致，余额独立使用全维度账号汇总', async () => {
    const wrapper = await mountView()
    await flushPromises()

    expect(getBusinessSummary).toHaveBeenCalledWith({ start_date: '2026-07-09', end_date: '2026-08-17' })
    expect(wrapper.get('[data-test="range-requests"]').text()).toContain('123')
    expect(wrapper.get('[data-test="range-tokens"]').text()).toContain('2.00K')
    expect(wrapper.get('[data-test="range-consumption"]').text()).toContain('¥20.00')
    expect(wrapper.get('[data-test="range-consumption"]').text()).toContain('¥18.00')
    expect(wrapper.get('[data-test="user-total-recharge"]').text()).toBe('¥120.00')
    expect(wrapper.get('[data-test="user-balance-total"]').text()).toBe('¥300.50')
    expect(wrapper.get('[data-test="upstream-recharge-total"]').text()).toBe('¥1,234.56')
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥328.00')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8.00')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥12.00')
    expect(wrapper.get('[data-test="recharge-difference"]').text()).toBe('¥-1,114.56')
    expect(wrapper.get('[data-test="user-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.snapshotTime')
    expect(wrapper.get('[data-test="upstream-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.balanceRefreshedAt')
    expect(wrapper.get('[data-test="ledger-status"]').text()).toContain('admin.businessHistory.requestCostNotice')
    expect(wrapper.get('[data-test="token-breakdown"]').text()).toContain('1.00K')
    expect(wrapper.get('[data-test="token-breakdown"]').text()).toContain('700')
  })

  it('选定日期即使存在资产区间估值，也优先使用请求记录上游消费', async () => {
    const wrapper = await mountView()
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8.00')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥12.00')
  })

  it('明确标注排除管理员后的消费口径', () => {
    expect(zhBusinessHistory.businessHistory.excludingAdmin).toBe("消费总额（已排除 admin{'@'}example.com）")
    expect(enBusinessHistory.businessHistory.excludingAdmin).toBe("消费总额（已排除 admin{'@'}example.com）")
    expect(zhBusinessHistory.businessHistory.userTotalRecharge).toBe('用户总充值')
    expect(zhBusinessHistory.businessHistory.upstreamRechargeTotal).toBe('上游总充值')
    expect(enBusinessHistory.businessHistory.upstreamRechargeTotal).toBe('上游总充值')
  })

  it('不再显示与页面标题重复的经营历史说明', async () => {
    const wrapper = await mountView()
    await flushPromises()

    expect(wrapper.text()).not.toContain('按日期查询永久保存的请求数、Token 和消费汇总')
    expect(wrapper.find('h1').exists()).toBe(false)
  })

  it('复用 AppLayout 的页面内边距', async () => {
    const wrapper = await mountView()
    await flushPromises()

    const content = wrapper.get('[data-test="business-history-content"]')
    expect(content.classes()).toContain('space-y-6')
    expect(content.classes().some((className) => /^(?:sm:)?p[xy]-/.test(className))).toBe(false)
  })

  it('按管理员选择的日期范围重新查询', async () => {
    const wrapper = await mountView()
    await flushPromises()

    await applyDates(wrapper, '2026-08-01', '2026-08-10')

    expect(getBusinessSummary).toHaveBeenLastCalledWith({ start_date: '2026-08-01', end_date: '2026-08-10' })
  })

  it('每日明细倒序、趋势图升序，累计收益读取全部历史账本口径', async () => {
    const wrapper = await mountView()
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

  it('区间余额弹窗也展示全维度账号余额，历史边界另作说明', async () => {
    const wrapper = await mountView()
    await flushPromises()
    await wrapper.get('[data-test="open-balance-details"]').trigger('click')
    await flushPromises()
    expect(assets).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="balance-detail-total"]').text()).toBe('¥328.00')
    expect(wrapper.get('[data-test="balance-detail-row"]').text()).toContain('up.example')
    expect(wrapper.get('[data-test="details-dialog"]').text()).toContain('admin.businessHistory.boundaryBalances')
  })

  it('充值弹窗使用已查询日期，输入框尚未查询的修改不影响明细', async () => {
    rechargeSummary.mockResolvedValue({ total_amount: 80, sites: [{ site_id: 1, site_name: '站点甲', total_amount: 80, items: [{ id: 1, occurred_at: '2026-08-16T01:00:00Z', amount: 80, record_type: 'recharge', note: '余额增量自动识别' }] }] })
    const wrapper = await mountView()
    await flushPromises()
    await editDates(wrapper, '2026-08-01')
    await wrapper.get('[data-test="open-recharge-details"]').trigger('click')
    await flushPromises()
    expect(rechargeSummary).toHaveBeenCalledWith({ start_time: '2026-07-09T00:00:00+08:00', end_time: '2026-08-17T23:59:59.999999+08:00' })
    expect(wrapper.get('[data-test="recharge-detail-row"]').text()).toContain('¥80.00')
    expect(wrapper.get('[data-test="details-dialog"]').text()).toContain('站点甲')
  })

  it('总收益弹窗保留已有累计值，缺失当天收益使用每日请求估算', async () => {
    const wrapper = await mountView()
    await flushPromises()
    await wrapper.get('[data-test="open-profit-details"]').trigger('click')
    const rows = wrapper.findAll('[data-test="profit-detail-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('2026-08-17')
    expect(rows[0].text()).toContain('¥500.00')
    expect(rows[0].text()).toContain('¥13,581.93')
    expect(rows[1].text()).toContain('¥5.00')
  })

  it('历史边界缺失用每日请求估算补消费收益，不用当前余额冒充历史余额', async () => {
    getBusinessSummary.mockResolvedValue({
      ...response,
      range_user_balance_total: null,
      range_upstream_balance_total: null,
      range_balance_snapshot_date: null,
      ledger: { ...response.ledger, opening_balance: null, closing_balance: null, range_consumption: null, range_profit: null, range_consumption_estimate: null, range_profit_estimate: null, range_status: 'missing_boundary' }
    })
    const wrapper = await mountView()
    await flushPromises()

    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8.00')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥12.00')
    expect(wrapper.get('[data-test="user-balance-total"]').text()).toBe('--')
    expect(wrapper.get('[data-test="upstream-balance-total"]').text()).toBe('¥328.00')
    expect(wrapper.get('[data-test="user-balance-snapshot-hint"]').text()).toBe('admin.businessHistory.balanceSnapshotEmpty')
  })

  it('截图中的请求估算超过充值时改用充值减期末余额，收益同步重算', async () => {
    getBusinessSummary.mockResolvedValue({
      ...response,
      range_upstream_recharge_total: 33712.97,
      range: { ...response.range, actual_cost: 58834.31, upstream_cost: 39984.81 },
      daily: [{ ...response.daily[0], upstream_cost: 39984.81 }],
      ledger: { ...response.ledger, opening_balance: null, closing_balance: 458.77, range_consumption_estimate: null, range_profit_estimate: null, range_status: 'missing_boundary' }
    })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥33,254.20')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥25,580.11')
    expect(wrapper.get('[data-test="ledger-status"]').text()).toBe('admin.businessHistory.rechargeBalanceFallbackNotice')
    // 原请求估算仅作明细参考，不篡改已有记录。
    expect(wrapper.get('[data-test="history-daily-row"]').text()).toContain('¥39,984.81')
  })

  it.each(['range_consumption_estimate', 'range_consumption'] as const)('资产区间 %s 超过充值时也采用同一差额公式', async field => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: {
      ...response.ledger, range_consumption_estimate: null, [field]: 1300
    }, range: { ...response.range, upstream_cost: 1300 } })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥574.31')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-554.31')
  })

  it('超额消费没有历史期末余额时不借用当前余额、不把缺失补零', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: {
      ...response.ledger, closing_balance: null, range_consumption_estimate: 1300
    }, range: { ...response.range, upstream_cost: 1300 } })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('--')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('--')
    expect(wrapper.get('[data-test="ledger-status"]').text()).toBe('admin.businessHistory.rechargeBalanceFallbackMissing')
  })

  it('超额消费的期末余额为零时正常使用差额公式', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: {
      ...response.ledger, closing_balance: 0, range_consumption_estimate: 1300
    }, range: { ...response.range, upstream_cost: 1300 } })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥1,234.56')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-1,214.56')
  })

  it('消费等于充值时保留原算法；切换日期后清除之前的差额兜底', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: { ...response.ledger, range_consumption_estimate: 1300 }, range: { ...response.range, upstream_cost: 1300 } })
    const wrapper = await mountView()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥574.31')
    getBusinessSummary.mockResolvedValue({ ...response, ledger: {
      ...response.ledger, range_consumption_estimate: 1234.56, range_profit_estimate: -1214.56
    }, range: { ...response.range, upstream_cost: 1234.56 } })
    await applyDates(wrapper, '2026-08-01', '2026-08-10')
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥1,234.56')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥-1,214.56')
    expect(wrapper.get('[data-test="ledger-status"]').text()).toBe('admin.businessHistory.requestCostNotice')
  })

  it('收益趋势同图展示全部账号与排除 admin 两条线', async () => {
    const wrapper = await mountView()
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

  it('零请求成本正常显示，不视为缺失', async () => {
    getBusinessSummary.mockResolvedValue({
      ...response,
      range_upstream_recharge_total: 120,
      range: { ...response.range, upstream_cost: 0 },
      ledger: { ...response.ledger, range_consumption_estimate: 0, range_profit_estimate: 0 }
    })
    const wrapper = await mountView()
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥0.00')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥20.00')
    expect(wrapper.get('[data-test="recharge-difference"]').text()).toBe('¥0.00')
  })

  it('站点范围变化时明确标记请求估算兜底', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, ledger: {
      ...response.ledger, range_status: 'site_scope_changed', range_consumption_estimate: null, range_profit_estimate: null
    } })
    const wrapper = await mountView()
    await flushPromises()
    expect(wrapper.get('[data-test="upstream-total-consumption"]').text()).toBe('¥8.00')
    expect(wrapper.get('[data-test="total-profit"]').text()).toBe('¥12.00')
    expect(wrapper.get('[data-test="ledger-status"]').text()).toBe('admin.businessHistory.requestCostNotice')
  })

  it('查询失败后保留上次成功区间，不把旧数字标成新日期', async () => {
    const wrapper = await mountView()
    await flushPromises()
    getBusinessSummary.mockRejectedValueOnce(new Error('读取失败'))
    await applyDates(wrapper, '2026-08-01', '2026-08-17')
    expect(wrapper.get('[data-test="applied-range"]').text()).toBe('2026-07-09 ～ 2026-08-17')
    expect(notifications.showError).toHaveBeenCalled()
  })

  it('没有每日汇总时显示历史暂无统计数据', async () => {
    getBusinessSummary.mockResolvedValue({ ...response, daily: [] })
    const wrapper = await mountView()
    await flushPromises()

    expect(wrapper.text()).toContain('admin.businessHistory.noData')
    expect(wrapper.find('[data-test="business-history-chart"]').exists()).toBe(false)
  })
})
