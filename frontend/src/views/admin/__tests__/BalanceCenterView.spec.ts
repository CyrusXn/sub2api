import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import BalanceCenterView from '../BalanceCenterView.vue'
import { reactive } from 'vue'

const route = reactive<{ query: Record<string, string> }>({ query: {} })
vi.mock('vue-router', () => ({ useRoute: () => route }))

const api = vi.hoisted(() => ({
  sites: vi.fn(), rechargeSummary: vi.fn(), createRechargeEvent: vi.fn(), deleteRechargeEvent: vi.fn(),
  getSettings: vi.fn(), updateSettings: vi.fn(), listUpstreamSites: vi.fn()
}))
const notifications = vi.hoisted(() => ({ showSuccess: vi.fn(), showError: vi.fn() }))

vi.mock('@/api/admin', () => ({ adminAPI: { balanceCenter: api, accounts: api } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => notifications }))
vi.mock('vue-i18n', async () => ({
  ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')),
  useI18n: () => ({ t: (key: string) => key })
}))

const sites = [
  { id: 1, name: 'HBY', normalized_domain: 'hby.example', base_url: 'https://hby.example', source: 'sub2api', probe_supported: true, updated_at: '2026-08-14T00:00:00Z', historical_recharge_total: 20 },
  { id: 2, name: 'VoVo', normalized_domain: 'vovo.example', base_url: 'https://vovo.example', source: 'sub2api', probe_supported: true, updated_at: '2026-08-14T00:00:00Z', historical_recharge_total: 80 }
]
const summary = {
  total_amount: 80,
  total: 2,
  page: 1,
  page_size: 20,
  items: [
    { id: 12, source: 'manual', source_key: 'tx-12', site_id: 1, amount: 50, currency: 'CNY', occurred_at: '2026-08-14T02:10:00Z', note: '' },
    { id: 11, source: 'manual', source_key: 'tx-11', site_id: 1, amount: 30, currency: 'CNY', occurred_at: '2026-08-13T02:10:00Z', note: '' }
  ],
  sites: [{ site_id: 1, site_name: 'HBY', total_amount: 80, record_count: 2, items: [
    { id: 12, source: 'manual', source_key: 'tx-12', site_id: 1, amount: 50, currency: 'CNY', occurred_at: '2026-08-14T02:10:00Z', note: '' },
    { id: 11, source: 'manual', source_key: 'tx-11', site_id: 1, amount: 30, currency: 'CNY', occurred_at: '2026-08-13T02:10:00Z', note: '' }
  ] }]
}

const mountView = (attachTo?: HTMLElement) => mount(BalanceCenterView, {
  attachTo,
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      GeminiInteractionReview: true,
      Pagination: true,
      Icon: true,
      RouterLink: { props: ['to'], template: '<a><slot /></a>' }
    }
  }
})

describe('BalanceCenterView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    route.query = {}
    vi.stubGlobal('confirm', vi.fn(() => true))
    api.sites.mockResolvedValue(sites)
    api.listUpstreamSites.mockResolvedValue([
      { host: 'hby.example', website_url: 'https://hby.example', account_ids: [10], account_names: [], login_username: '', has_password: false, protocol: 'innom' },
      { host: 'vovo.example', website_url: 'https://vovo.example', account_ids: [20], account_names: [], login_username: '', has_password: false, protocol: 'innom' }
    ])
    api.rechargeSummary.mockResolvedValue(summary)
    api.createRechargeEvent.mockResolvedValue(summary.items[0])
    api.deleteRechargeEvent.mockResolvedValue(undefined)
    api.getSettings.mockResolvedValue({ enabled: true, event_probe_enabled: true, email_enabled: true, low_balance_threshold: 5 })
    api.updateSettings.mockImplementation(async settings => settings)
  })

  it('从提醒定位站点、按充值档位预填10并选中输入文本，不自动入账', async () => {
    route.query = { recharge_host: 'HBY.EXAMPLE', recharge_delta: '8.96337526', recharge_currency: 'USD', recharge_request: 'first' }
    api.rechargeSummary.mockResolvedValue({ ...summary, items: [
      { ...summary.items[0], amount: 100, record_type: 'recharge' },
      { ...summary.items[1], amount: 10, record_type: 'recharge' }
    ] })
    const select = vi.spyOn(HTMLInputElement.prototype, 'select')
    const wrapper = mountView(document.body)
    await flushPromises()
    const input = wrapper.get<HTMLInputElement>('[data-test="recharge-amount-1"]')
    expect(input.element.value).toBe('10')
    expect(document.activeElement).toBe(input.element)
    expect(input.element.selectionStart).toBe(0)
    expect(input.element.selectionEnd).toBe(2)
    expect(select).toHaveBeenCalled()
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="recharge-suggestion"]').text()).toContain('admin.balanceCenter.reminder.suggestion')
    expect(api.createRechargeEvent).not.toHaveBeenCalled()
    wrapper.unmount()
    select.mockRestore()
  })

  it('已在余额中心时仍响应新的点击，切回录入页并向上匹配充值档位', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    route.query = { recharge_host: 'vovo.example', recharge_delta: '8.96', recharge_currency: 'USD', recharge_request: 'second' }
    await flushPromises()
    expect(wrapper.get('[data-test="tab-add-recharge"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get<HTMLInputElement>('[data-test="recharge-amount-2"]').element.value).toBe('10')
    expect(api.createRechargeEvent).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('重复点击同一提醒会重新选中输入框，但保留手工修正金额', async () => {
    route.query = { recharge_host: 'hby.example', recharge_delta: '8.96', recharge_currency: 'USD', recharge_request: 'third' }
    const wrapper = mountView(document.body)
    await flushPromises()
    const input = wrapper.get<HTMLInputElement>('[data-test="recharge-amount-1"]')
    expect(input.element.value).toBe('10')
    await input.setValue('12')
    const other = wrapper.get<HTMLInputElement>('[data-test="recharge-amount-2"]')
    other.element.focus()
    route.query = { ...route.query, recharge_request: 'third-again' }
    await flushPromises()
    expect(input.element.value).toBe('12')
    expect(document.activeElement).toBe(input.element)
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each([
    [1, '1'], [1.01, '5'], [2.6, '5'], [5, '5'], [5.01, '10'], [8.96, '10'],
    [10, '10'], [15.6, '20'], [20, '20'], [40.8, '50'], [50, '50'],
    [96.2, '100'], [100, '100'], [198.5, '200'], [200, '200'], [201, '300'], [300, '300'], [450, '500'], [500, '500']
  ])('净增 %s 时按固定充值档位建议 %s', async (delta, expected) => {
    route.query = { recharge_host: 'hby.example', recharge_delta: String(delta), recharge_request: String(delta) }
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-test="recharge-amount-1"]').element.value).toBe(expected)
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    expect(api.createRechargeEvent).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each([
    [430.1590791, 0.1, '50'],
    [96.2, 0.1, '10'],
    [8.96, 0.1, '1'],
    [8.96, 2, '20'],
    [500, 0.1, '50'],
    [430.1590791, 1, '500'],
    [430.1590791, undefined, '500'],
    [430.1590791, 0, '500'],
    [430.1590791, -1, '500'],
    [430.1590791, Number.NaN, '500'],
    [430.1590791, Number.POSITIVE_INFINITY, '500']
  ])('净增 %s、充值换算系数 %s 时先换算再按档位建议 %s', async (delta, scale, expected) => {
    api.listUpstreamSites.mockResolvedValue([
      { host: 'HBY.EXAMPLE', account_ids: [10], recharge_conversion_scale: scale }
    ])
    route.query = { recharge_host: 'hby.example', recharge_delta: String(delta), recharge_currency: 'USD' }
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-test="recharge-amount-1"]').element.value).toBe(expected)
    expect(api.createRechargeEvent).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('非法提醒参数或未配置站点不会预填和查询历史', async () => {
    route.query = { recharge_host: 'missing.example', recharge_delta: '8.96' }
    const wrapper = mountView()
    await flushPromises()
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    route.query = { recharge_host: 'hby.example', recharge_delta: 'Infinity' }
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-test="recharge-amount-1"]').element.value).toBe('')
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('允许普通小数手工修正，但拒绝非十进制金额', async () => {
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get('[data-test="recharge-amount-1"]')
    await input.setValue('0x10')
    await input.trigger('keyup.enter')
    expect(api.createRechargeEvent).not.toHaveBeenCalled()
    await input.setValue('8.5')
    await input.trigger('keyup.enter')
    expect(api.createRechargeEvent).toHaveBeenCalledWith(expect.objectContaining({ amount: 8.5 }))
    wrapper.unmount()
  })

  it('默认只展示新增充值并仅加载站点', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(api.sites).toHaveBeenCalledOnce()
    expect(api.listUpstreamSites).toHaveBeenCalledWith({ include_history: false })
    expect(api.getSettings).toHaveBeenCalledOnce()
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    expect(wrapper.get('[data-test="tab-add-recharge"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.findAll('[data-test="site-recharge-row"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="total-amount"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="recharge-list-scroll"]').classes()).toContain('overflow-y-auto')
    expect(wrapper.findAll('[data-test="site-recharge-row"]')[0].text()).toContain('VoVo')
  })

  it('新增充值只显示当前账号使用的站点，历史统计仍按原参数加载全部站点', async () => {
    api.listUpstreamSites.mockResolvedValue([
      { host: 'hby.example', website_url: 'https://hby.example', account_ids: [10], account_names: [], login_username: '', has_password: false, protocol: 'innom' }
    ])
    const wrapper = mountView()
    await flushPromises()

    const addRows = wrapper.findAll('[data-test="site-recharge-row"]')
    expect(addRows).toHaveLength(1)
    expect(addRows[0].text()).toContain('HBY')

    api.rechargeSummary.mockResolvedValue({
      ...summary,
      sites: [
        ...summary.sites,
        { site_id: 2, site_name: 'VoVo', total_amount: 30, record_count: 1, items: [] }
      ]
    })
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    expect(api.rechargeSummary).toHaveBeenCalledWith(expect.objectContaining({
      page: 1,
      page_size: 20,
      start_time: expect.any(String),
      end_time: expect.any(String)
    }))
    expect(api.rechargeSummary.mock.calls[0][0]).not.toHaveProperty('site_id')

    await wrapper.get('[data-test="dimension-site"]').trigger('click')
    expect(wrapper.findAll('[data-test="site-summary-row"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('VoVo')
  })

  it('在余额中心直接设置低余额邮件阈值', async () => {
    const wrapper = mountView()
    await flushPromises()

    const threshold = wrapper.get('[data-test="low-balance-threshold"]')
    expect((threshold.element as HTMLInputElement).value).toBe('5')
    expect(wrapper.get('[data-test="balance-email-enabled"]').attributes('aria-checked')).toBe('true')

    await threshold.setValue('8.5')
    await wrapper.get('[data-test="save-alert-settings"]').trigger('click')
    await flushPromises()

    expect(api.updateSettings).toHaveBeenCalledWith({
      enabled: true,
      event_probe_enabled: true,
      email_enabled: true,
      low_balance_threshold: 8.5
    })
    expect(notifications.showSuccess).toHaveBeenCalledWith('admin.balanceCenter.alertSettingsSaved')
  })

  it('低余额阈值为空时拒绝保存', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="low-balance-threshold"]').setValue('')
    await wrapper.get('[data-test="save-alert-settings"]').trigger('click')
    await flushPromises()

    expect(api.updateSettings).not.toHaveBeenCalled()
    expect(notifications.showError).toHaveBeenCalledWith('admin.balanceCenter.invalidLowBalanceThreshold')
  })

  it('告警设置加载失败时仍展示站点列表', async () => {
    api.getSettings.mockRejectedValue(new Error('settings unavailable'))

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('[data-test="site-recharge-row"]')).toHaveLength(2)
    expect(wrapper.find('[data-test="low-balance-threshold"]').exists()).toBe(false)
    expect(notifications.showError).toHaveBeenCalled()
  })

  it('新增充值和充值记录都使用不透明斑马纹', async () => {
    const wrapper = mountView()
    await flushPromises()

    const rechargeRows = wrapper.findAll('[data-test="site-recharge-row"]')
    expect(rechargeRows[0].classes()).toEqual(expect.arrayContaining(['odd:bg-gray-50', 'even:bg-white']))

    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="dimension-site"]').trigger('click')
    await wrapper.get('[data-test="expand-site-1"]').trigger('click')

    const historyRows = wrapper.findAll('[data-test="site-history-item"]')
    expect(historyRows).toHaveLength(2)
    expect(historyRows[0].classes()).toEqual(expect.arrayContaining(['odd:bg-gray-50', 'even:bg-white']))
    expect(historyRows[0].classes().some(className => className.includes('/'))).toBe(false)
  })

  it('回到此刻按钮将充值时间重置为当前秒', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="recharge-time-now"]').trigger('click')
    // datetime-local 会省略值为 00 的秒；核对实际时间，避免整分钟随机失败。
    const value = (wrapper.get('[data-test="recharge-time"]').element as HTMLInputElement).value
    expect(Math.abs(Date.now() - new Date(value).getTime())).toBeLessThan(2000)
  })

  it('切换到充值记录时才加载汇总并隐藏新增充值', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()

    expect(api.rechargeSummary).toHaveBeenCalledOnce()
    expect(wrapper.get('[data-test="tab-recharge-records"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-test="total-amount"]').text()).toContain('80.00')
    expect(wrapper.find('[data-test="site-recharge-row"]').exists()).toBe(false)
  })

  it('站点金额回车后使用当前秒新增并刷新统计', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="recharge-time"]').setValue('2026-08-14T10:20')
    const input = wrapper.get('[data-test="recharge-amount-1"]')
    await input.setValue('88.5')
    await input.trigger('keyup.enter')
    await flushPromises()

    expect(api.createRechargeEvent).toHaveBeenCalledWith(expect.objectContaining({
      source: 'manual', site_id: 1, amount: 88.5, currency: 'CNY', occurred_at: '2026-08-14T02:20:00.000Z'
    }))
    expect(api.rechargeSummary).not.toHaveBeenCalled()
    expect((input.element as HTMLInputElement).value).toBe('')
  })

  it('选择快捷时间后直接刷新且无需确定按钮', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="range-yesterday"]').trigger('click')
    await flushPromises()

    expect(api.rechargeSummary).toHaveBeenCalledTimes(2)
    const params = api.rechargeSummary.mock.calls[1][0]
    expect(params.start_time).toBeTruthy()
    expect(params.end_time).toBeTruthy()
    expect(wrapper.find('[data-test="apply-range"]').exists()).toBe(false)
  })

  it('时间维度删除后重查总额，站点维度可展开多个时间节点', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="delete-recharge-12"]').trigger('click')
    await flushPromises()

    expect(api.deleteRechargeEvent).toHaveBeenCalledWith(12)
    expect(api.rechargeSummary).toHaveBeenCalledTimes(2)

    await wrapper.get('[data-test="dimension-site"]').trigger('click')
    await wrapper.get('[data-test="expand-site-1"]').trigger('click')
    expect(wrapper.findAll('[data-test="site-history-item"]')).toHaveLength(2)
    expect(wrapper.text()).toContain('HBY')
  })

  it('站点维度按筛选范围内充值总额降序展示', async () => {
    api.rechargeSummary.mockResolvedValue({
      ...summary,
      sites: [
        { site_id: 1, site_name: 'HBY', total_amount: 20, record_count: 1, items: [] },
        { site_id: 2, site_name: 'VoVo', total_amount: 80, record_count: 1, items: [] }
      ]
    })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="dimension-site"]').trigger('click')
    expect(wrapper.findAll('[data-test="site-summary-row"]')[0].text()).toContain('VoVo')
  })

  it('未绑定账号的旧站点仍按独立标签展示', async () => {
    api.rechargeSummary.mockResolvedValue({
      ...summary,
      total_amount: 10,
      total: 1,
      items: [{ id: 21, source: 'legacy_opening', source_key: 'opening:fox', site_label: 'Fox', amount: 10, currency: 'CNY', occurred_at: '2026-08-11T00:00:00Z', note: '旧系统期初累计，历史日期未知' }],
      sites: [{ site_name: 'Fox', total_amount: 10, record_count: 1, items: [] }]
    })

    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="tab-recharge-records"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Fox')
    expect(wrapper.text()).toContain('admin.balanceCenter.unknownRechargeDate')
    expect(wrapper.text()).not.toContain('2026-08-11')
    expect(wrapper.text()).not.toContain('admin.balanceCenter.unassignedSite')
  })
})
